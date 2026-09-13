package tests

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/server/common/persistence/sql/sqlplugin"
	"go.temporal.io/server/common/primitives"
)

type (
	historyExecutionChasmSuite struct {
		suite.Suite
		*require.Assertions

		store sqlplugin.HistoryExecutionChasm

		// doubleCountsUpdatedRows records that this store reports an updated row
		// twice in RowsAffected, so the replace counts cannot be compared.
		doubleCountsUpdatedRows bool
	}

	// HistoryExecutionChasmSuiteOption configures the suite for a store's quirks.
	HistoryExecutionChasmSuiteOption func(*historyExecutionChasmSuite)
)

// WithDoubleCountedUpdatedRows declares that the store under test counts an
// updated row twice in the RowsAffected of an upsert.
//
// MySQL and MariaDB both do this: we set clientFoundRows on the session
// (common/persistence/sql/sqlplugin/mysql/session/session.go), which makes
// `INSERT ... ON DUPLICATE KEY UPDATE` report 2 for every row it updates.
// https://dev.mysql.com/doc/refman/8.4/en/information-functions.html#function_row-count
func WithDoubleCountedUpdatedRows() HistoryExecutionChasmSuiteOption {
	return func(s *historyExecutionChasmSuite) {
		s.doubleCountsUpdatedRows = true
	}
}

func NewHistoryExecutionChasmSuite(
	t *testing.T,
	store sqlplugin.HistoryExecutionChasm,
	opts ...HistoryExecutionChasmSuiteOption,
) *historyExecutionChasmSuite {
	s := &historyExecutionChasmSuite{
		Assertions: require.New(t),
		store:      store,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *historyExecutionChasmSuite) SetupTest() {
	s.Assertions = require.New(s.T())
}

type testCase struct {
	InsertRows    []sqlplugin.ChasmNodeMapsRow
	ReplaceRows   []sqlplugin.ChasmNodeMapsRow // executed after InsertRows to test update functionality
	DeleteRows    *sqlplugin.ChasmNodeMapsFilter
	DeleteAllRows *sqlplugin.ChasmNodeMapsAllFilter

	ExpectedRowsFilter sqlplugin.ChasmNodeMapsAllFilter // used to selected the rows compared against ExpectedRows
	ExpectedRows       []sqlplugin.ChasmNodeMapsRow     // asserted after all mutations are completed
}

func (s *historyExecutionChasmSuite) runTestCase(tc *testCase) {
	ctx := newExecutionContext()

	if len(tc.InsertRows) > 0 {
		res, err := s.store.ReplaceIntoChasmNodeMaps(ctx, tc.InsertRows)
		s.NoError(err)
		affected, err := res.RowsAffected()
		s.NoError(err)
		s.Equal(int64(len(tc.InsertRows)), affected)
	}

	// Inserts and Replaces are applied identically, but in sequence.
	if len(tc.ReplaceRows) > 0 {
		res, err := s.store.ReplaceIntoChasmNodeMaps(ctx, tc.ReplaceRows)
		s.NoError(err)
		affected, err := res.RowsAffected()
		s.NoError(err)

		// See WithDoubleCountedUpdatedRows: on those stores the count is not
		// comparable because updated rows are counted twice.
		if !s.doubleCountsUpdatedRows {
			s.Equal(int64(len(tc.ReplaceRows)), affected)
		}
	}

	if tc.DeleteRows != nil {
		res, err := s.store.DeleteFromChasmNodeMaps(ctx, *tc.DeleteRows)
		s.NoError(err)
		affected, err := res.RowsAffected()
		s.NoError(err)
		s.Equal(int64(len(tc.DeleteRows.ChasmPaths)), affected)
	}

	if tc.DeleteAllRows != nil {
		_, err := s.store.DeleteAllFromChasmNodeMaps(ctx, *tc.DeleteAllRows)
		s.NoError(err)
	}

	// Verify expected rows are set after all mutations are applied.
	actualRows, err := s.store.SelectAllFromChasmNodeMaps(ctx, tc.ExpectedRowsFilter)
	s.NoError(err)
	s.ElementsMatch(tc.ExpectedRows, actualRows)
}

func newChasmNodeMapsAllFilter() sqlplugin.ChasmNodeMapsAllFilter {
	return sqlplugin.ChasmNodeMapsAllFilter{
		ShardID:     rand.Int32(),
		NamespaceID: primitives.NewUUID(),
		WorkflowID:  primitives.NewUUID().String(),
		RunID:       primitives.NewUUID(),
	}
}

func newChasmRowFromFilter(filter sqlplugin.ChasmNodeMapsAllFilter) sqlplugin.ChasmNodeMapsRow {
	return sqlplugin.ChasmNodeMapsRow{
		ShardID:          filter.ShardID,
		NamespaceID:      filter.NamespaceID,
		WorkflowID:       filter.WorkflowID,
		RunID:            filter.RunID,
		ChasmPath:        primitives.NewUUID().String(),
		Metadata:         []byte(primitives.NewUUID().String()),
		MetadataEncoding: "utf8",
		Data:             []byte(primitives.NewUUID().String()),
		DataEncoding:     "utf8",
	}
}

func (s *historyExecutionChasmSuite) TestInsert() {
	filter := newChasmNodeMapsAllFilter()

	var expectedRows []sqlplugin.ChasmNodeMapsRow
	for range 10 {
		expectedRows = append(expectedRows, newChasmRowFromFilter(filter))
	}

	tc := &testCase{
		InsertRows:         expectedRows,
		ExpectedRowsFilter: filter,
		ExpectedRows:       expectedRows,
	}
	s.runTestCase(tc)
}

func (s *historyExecutionChasmSuite) TestUpdate() {
	filter := newChasmNodeMapsAllFilter()

	var initialRows []sqlplugin.ChasmNodeMapsRow
	for range 10 {
		initialRows = append(initialRows, newChasmRowFromFilter(filter))
	}

	// Update first half of inserted rows.
	pivot := len(initialRows) / 2
	updatedRows := make([]sqlplugin.ChasmNodeMapsRow, pivot)
	for i, row := range initialRows[:pivot] {
		path := row.ChasmPath
		updatedRows[i] = newChasmRowFromFilter(filter)
		updatedRows[i].ChasmPath = path
	}
	unchangedRows := initialRows[pivot:]
	expectedRows := append(updatedRows, unchangedRows...)
	s.NotElementsMatch(initialRows, expectedRows)

	tc := &testCase{
		InsertRows:         initialRows,
		ReplaceRows:        updatedRows,
		ExpectedRowsFilter: filter,
		ExpectedRows:       expectedRows,
	}
	s.runTestCase(tc)
}

func (s *historyExecutionChasmSuite) TestDelete() {
	filter := newChasmNodeMapsAllFilter()

	var initialRows []sqlplugin.ChasmNodeMapsRow
	for range 10 {
		initialRows = append(initialRows, newChasmRowFromFilter(filter))
	}

	// Delete half of inserted rows.
	pivot := len(initialRows) / 2
	var deletedPaths []string
	for _, row := range initialRows[pivot:] {
		deletedPaths = append(deletedPaths, row.ChasmPath)
	}
	expectedRows := initialRows[:pivot]

	tc := &testCase{
		InsertRows: initialRows,
		DeleteRows: &sqlplugin.ChasmNodeMapsFilter{
			ShardID:     filter.ShardID,
			NamespaceID: filter.NamespaceID,
			WorkflowID:  filter.WorkflowID,
			RunID:       filter.RunID,
			ChasmPaths:  deletedPaths,
		},
		ExpectedRowsFilter: filter,
		ExpectedRows:       expectedRows,
	}
	s.runTestCase(tc)
}

func (s *historyExecutionChasmSuite) TestDeleteAll() {
	filter := newChasmNodeMapsAllFilter()

	var initialRows []sqlplugin.ChasmNodeMapsRow
	for range 10 {
		initialRows = append(initialRows, newChasmRowFromFilter(filter))
	}

	tc := &testCase{
		InsertRows:         initialRows,
		DeleteAllRows:      &filter,
		ExpectedRowsFilter: filter,
		ExpectedRows:       nil,
	}
	s.runTestCase(tc)
}

func (s *historyExecutionChasmSuite) TestInsertReplaceDelete() {
	filter := newChasmNodeMapsAllFilter()

	// Insert 15 rows.
	var initialRows []sqlplugin.ChasmNodeMapsRow
	for range 15 {
		initialRows = append(initialRows, newChasmRowFromFilter(filter))
	}

	// Delete a third of the inserted rows.
	pivot := len(initialRows) / 3
	var deletedPaths []string
	for _, row := range initialRows[:pivot] {
		deletedPaths = append(deletedPaths, row.ChasmPath)
	}

	// Replace another third of the inserted rows.
	updatedRows := make([]sqlplugin.ChasmNodeMapsRow, pivot)
	for i := range pivot {
		path := initialRows[i+pivot].ChasmPath
		updatedRows[i] = newChasmRowFromFilter(filter)
		updatedRows[i].ChasmPath = path
	}

	// Last third of initial batch, and updated rows, remain.
	expectedRows := append(initialRows[pivot*2:], updatedRows...)

	tc := &testCase{
		InsertRows:  initialRows,
		ReplaceRows: updatedRows,
		DeleteRows: &sqlplugin.ChasmNodeMapsFilter{
			ShardID:     filter.ShardID,
			NamespaceID: filter.NamespaceID,
			WorkflowID:  filter.WorkflowID,
			RunID:       filter.RunID,
			ChasmPaths:  deletedPaths,
		},
		ExpectedRowsFilter: filter,
		ExpectedRows:       expectedRows,
	}
	s.runTestCase(tc)
}
