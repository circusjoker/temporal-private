"""Run an agentic (OpenAI Agents SDK) workflow against a MariaDB-backed Temporal
server and check that the whole loop is durable.

Two model backends:

    --model mock     (default) a scripted model. No LLM, no network, deterministic,
                     runs in about a second. This is the one to use in CI or to
                     check a MariaDB change, because a real model makes the run
                     non-deterministic: the same prompt has produced 23, 29 and 35
                     event histories here.

    --model ollama   a real local model through Ollama, matching the official
                     sample (samples-python/openai_agents/model_providers). Use it
                     to confirm the mock is not hiding anything.

What it asserts either way: the workflow completes, the agent really called its
tool, and the tool's output is in the history that came back out of MariaDB.

Note the tool is `async def`. openai-agents 0.19.4 runs a *synchronous*
@function_tool through asyncio.to_thread -> loop.run_in_executor, which Temporal's
deterministic workflow event loop rejects with NotImplementedError. That is not a
storage problem -- the unmodified official sample fails the same way on a stock
sqlite dev server.

    uv run python develop/mariadb/agentic_check.py [--model mock|ollama] [--address host:port]
"""

from __future__ import annotations

import argparse
import asyncio
import sys
import uuid
from datetime import timedelta
from typing import Any, Optional

from agents import (
    Agent,
    Model,
    ModelProvider,
    ModelResponse,
    ModelTracing,
    OpenAIChatCompletionsModel,
    Runner,
    function_tool,
    set_tracing_disabled,
)
from agents.usage import Usage
from openai.types.responses import (
    ResponseFunctionToolCall,
    ResponseOutputMessage,
    ResponseOutputText,
)
from temporalio import workflow
from temporalio.client import Client
from temporalio.contrib.openai_agents import ModelActivityParameters, OpenAIAgentsPlugin
from temporalio.worker import Worker

TASK_QUEUE = "mariadb-agentic-check"
TOOL_OUTPUT = "The weather in Tokyo is sunny."
FINAL_OUTPUT = "Tokyo sky is clear, sunlight rests on quiet streets, the tool has spoken."


@workflow.defn
class AgenticCheckWorkflow:
    @workflow.run
    async def run(self, prompt: str) -> str:
        @function_tool
        async def get_weather(city: str) -> str:
            workflow.logger.debug(f"Getting weather for {city}")
            return f"The weather in {city} is sunny."

        agent = Agent(
            name="Assistant",
            instructions=(
                "You only respond in haikus. When asked about the weather always "
                "use the tool to get the current weather."
            ),
            tools=[get_weather],
        )
        result = await Runner.run(agent, prompt)
        return result.final_output


class ScriptedModel(Model):
    """A model that calls the tool once and then answers.

    It decides which turn it is on by looking for a tool result in the input it
    was given, so it exercises the same two-round-trip agent loop a real model
    does -- and therefore the same two model activities in the workflow history.
    """

    async def get_response(
        self,
        system_instructions: Optional[str],
        input: Any,
        model_settings: Any,
        tools: Any,
        output_schema: Any,
        handoffs: Any,
        tracing: ModelTracing,
        *,
        previous_response_id: Optional[str] = None,
        conversation_id: Optional[str] = None,
        prompt: Any = None,
    ) -> ModelResponse:
        if _has_tool_result(input):
            message = ResponseOutputMessage(
                id="__scripted__",
                content=[
                    ResponseOutputText(text=FINAL_OUTPUT, type="output_text", annotations=[])
                ],
                role="assistant",
                status="completed",
                type="message",
            )
            return ModelResponse(output=[message], usage=Usage(), response_id=None)

        call = ResponseFunctionToolCall(
            id="__scripted__",
            call_id="call_scripted_1",
            name="get_weather",
            arguments='{"city":"Tokyo"}',
            type="function_call",
        )
        return ModelResponse(output=[call], usage=Usage(), response_id=None)

    def stream_response(self, *args: Any, **kwargs: Any) -> Any:
        raise NotImplementedError("the scripted model does not stream")


def _has_tool_result(input: Any) -> bool:
    if not isinstance(input, list):
        return False
    for item in input:
        if isinstance(item, dict) and item.get("type") == "function_call_output":
            return True
        if getattr(item, "type", None) == "function_call_output":
            return True
    return False


class ScriptedProvider(ModelProvider):
    def get_model(self, model_name: Optional[str]) -> Model:
        return ScriptedModel()


class OllamaProvider(ModelProvider):
    def __init__(self, model: str, base_url: str) -> None:
        from openai import AsyncOpenAI

        self._model = model
        self._client = AsyncOpenAI(base_url=base_url, api_key="ollama")

    def get_model(self, model_name: Optional[str]) -> Model:
        return OpenAIChatCompletionsModel(
            model=model_name or self._model, openai_client=self._client
        )


async def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", choices=["mock", "ollama"], default="mock")
    parser.add_argument("--address", default="127.0.0.1:7233")
    parser.add_argument("--namespace", default="default")
    parser.add_argument("--ollama-model", default="gpt-oss:20b")
    parser.add_argument("--ollama-url", default="http://localhost:11434/v1")
    args = parser.parse_args()

    set_tracing_disabled(True)

    if args.model == "mock":
        provider: ModelProvider = ScriptedProvider()
        timeout = timedelta(seconds=30)
    else:
        provider = OllamaProvider(args.ollama_model, args.ollama_url)
        timeout = timedelta(seconds=300)

    client = await Client.connect(
        args.address,
        namespace=args.namespace,
        plugins=[
            OpenAIAgentsPlugin(
                model_params=ModelActivityParameters(start_to_close_timeout=timeout),
                model_provider=provider,
            )
        ],
    )

    workflow_id = f"mariadb-agentic-check-{args.model}-{uuid.uuid4().hex[:8]}"
    async with Worker(
        client, task_queue=TASK_QUEUE, workflows=[AgenticCheckWorkflow]
    ):
        result = await client.execute_workflow(
            AgenticCheckWorkflow.run,
            "What's the weather in Tokyo?",
            id=workflow_id,
            task_queue=TASK_QUEUE,
        )

    handle = client.get_workflow_handle(workflow_id)
    desc = await handle.describe()

    # Read the history back out of the store and look for the tool round trip,
    # rather than trusting the in-process result.
    tool_calls: list[str] = []
    tool_outputs: list[str] = []
    events = 0
    async for event in handle.fetch_history_events():
        events += 1
        payloads = None
        if event.HasField("activity_task_scheduled_event_attributes"):
            payloads = event.activity_task_scheduled_event_attributes.input.payloads
        elif event.HasField("activity_task_completed_event_attributes"):
            payloads = event.activity_task_completed_event_attributes.result.payloads
        if not payloads:
            continue
        blob = payloads[0].data.decode("utf-8", errors="replace")
        if '"function_call"' in blob and "get_weather" in blob:
            tool_calls.append("get_weather")
        if TOOL_OUTPUT in blob:
            tool_outputs.append(TOOL_OUTPUT)

    print(f"model          : {args.model}")
    print(f"workflow       : {workflow_id}")
    print(f"status         : {desc.status.name}")
    print(f"history events : {events}")
    print(f"tool calls     : {len(tool_calls)}")
    print(f"tool output in history: {bool(tool_outputs)}")
    print(f"result         : {result!r}")

    problems = []
    if desc.status.name != "COMPLETED":
        problems.append(f"workflow status is {desc.status.name}, not COMPLETED")
    if not tool_calls:
        problems.append("the agent never called get_weather")
    if not tool_outputs:
        problems.append(f"{TOOL_OUTPUT!r} is not in the history read back from the store")
    if args.model == "mock" and result != FINAL_OUTPUT:
        problems.append(f"scripted model should have produced {FINAL_OUTPUT!r}, got {result!r}")

    if problems:
        for p in problems:
            print(f"FAIL: {p}")
        return 1
    print("OK")
    return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
