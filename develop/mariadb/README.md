# Checking Temporal on MariaDB

```bash
docker compose -f develop/docker-compose/docker-compose.yml up mariadb   # host port 3307
make install-schema-mariadb
make start-mariadb
```

## Agentic workflow check

`agentic_check.py` runs an OpenAI Agents SDK workflow against the server and checks the
loop is durable: the workflow completes, the agent really called its tool, and the tool's
output is in the history read back out of MariaDB.

```bash
# deterministic, no LLM, no network, ~1s -- use this in CI
uv run python develop/mariadb/agentic_check.py

# the same check against a real local model
ollama pull gpt-oss:20b
uv run python develop/mariadb/agentic_check.py --model ollama
```

Run it from a checkout of [samples-python](https://github.com/temporalio/samples-python)
(`uv sync --group openai-agents`), or any environment with `temporalio[openai-agents]`.

**Why a scripted model is the default.** A real model makes the run
non-deterministic — the same prompt produced 23-, 29- and 35-event histories here — so it
cannot tell you whether a storage change broke something. The scripted model takes the
same two turns (tool call, then answer) and produces the same 17-event history every
time. The `--model ollama` path exists to confirm the mock is not hiding anything; both
were verified to produce 17 events and pass identical assertions.

**Why the tool is `async def`.** openai-agents 0.19.4 runs a *synchronous*
`@function_tool` through `asyncio.to_thread` → `loop.run_in_executor`, which Temporal's
deterministic workflow event loop rejects with `NotImplementedError`. The official
`samples-python/openai_agents/model_providers` sample has a sync tool and so its tool call
fails — on any store, including a stock sqlite dev server. Not a MariaDB problem.

## Connection sizing

MariaDB's default `max_connections` is 151. A Temporal host holds `maxConns` plus the
visibility store's `maxConns` (20 + 2 in `config/development-mariadb.yaml`), so a handful
of hosts, or the functional test suite, will exhaust it — the symptom is
`no usable database connection found` and a climbing
`Connection_errors_max_connections`. The dev compose file sets `--max-connections=1000`.
