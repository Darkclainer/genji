package parser

import (
	"testing"

	"github.com/genjidb/genji/sql/planner"
	"github.com/genjidb/genji/sql/query/expr"
	"github.com/stretchr/testify/require"
)

func TestParserUpdate(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		expected *planner.Tree
		errored  bool
	}{
		{"SET/No cond", "UPDATE test SET a = 1",
			planner.NewTree(
				planner.NewReplacementNode(
					planner.NewSetNode(
						planner.NewTableInputNode("test"),
						parsePath(t, "a"), expr.IntegerValue(1),
					),
					"test",
				)),
			false},
		{"SET/With cond", "UPDATE test SET a = 1, b = 2 WHERE age = 10",
			planner.NewTree(
				planner.NewReplacementNode(
					planner.NewSetNode(
						planner.NewSetNode(
							planner.NewSelectionNode(
								planner.NewTableInputNode("test"),
								expr.Eq(expr.Path(parsePath(t, "age")), expr.IntegerValue(10)),
							),
							parsePath(t, "a"), expr.IntegerValue(1),
						),
						parsePath(t, "b"), expr.IntegerValue(2),
					),
					"test",
				)),
			false},
		{"SET/No cond path with backquotes", "UPDATE test SET `   some \"path\" ` = 1",
			planner.NewTree(
				planner.NewReplacementNode(
					planner.NewSetNode(
						planner.NewTableInputNode("test"),
						parsePath(t, "`   some \"path\" `"), expr.IntegerValue(1),
					),
					"test",
				)),
			false},
		{"SET/No cond nested path", "UPDATE test SET a.b = 1",
			planner.NewTree(
				planner.NewReplacementNode(
					planner.NewSetNode(
						planner.NewTableInputNode("test"),
						parsePath(t, "a.b"), expr.IntegerValue(1),
					),
					"test",
				)),
			false},
		{"SET/No cond nested path with backquotes", "UPDATE test SET a.` b `.c = 1",
			planner.NewTree(
				planner.NewReplacementNode(
					planner.NewSetNode(
						planner.NewTableInputNode("test"),
						parsePath(t, "a.` b `.c"), expr.IntegerValue(1),
					),
					"test",
				)),
			false},
		{"SET/No cond array index", "UPDATE test SET a[1] = 1",
			planner.NewTree(
				planner.NewReplacementNode(
					planner.NewSetNode(
						planner.NewTableInputNode("test"),
						parsePath(t, "a[1]"), expr.IntegerValue(1),
					),
					"test",
				)),
			false},
		{"SET/No cond nested array index", "UPDATE test SET a.b[100][10].c = 1",
			planner.NewTree(
				planner.NewReplacementNode(
					planner.NewSetNode(
						planner.NewTableInputNode("test"),
						parsePath(t, "a.b[100][10].c"), expr.IntegerValue(1),
					),
					"test",
				)),
			false},
		{"UNSET/No cond", "UPDATE test UNSET a",
			planner.NewTree(
				planner.NewReplacementNode(
					planner.NewUnsetNode(
						planner.NewTableInputNode("test"),
						"a",
					),
					"test",
				)),
			false},
		{"UNSET/With cond", "UPDATE test UNSET a, b WHERE age = 10",
			planner.NewTree(
				planner.NewReplacementNode(
					planner.NewUnsetNode(
						planner.NewUnsetNode(
							planner.NewSelectionNode(
								planner.NewTableInputNode("test"),
								expr.Eq(expr.Path(parsePath(t, "age")), expr.IntegerValue(10)),
							),
							"a",
						),
						"b",
					),
					"test",
				)),
			false},
		{"Trailing comma", "UPDATE test SET a = 1, WHERE age = 10", nil, true},
		{"No SET", "UPDATE test WHERE age = 10", nil, true},
		{"No pair", "UPDATE test SET WHERE age = 10", nil, true},
		{"query.Field only", "UPDATE test SET a WHERE age = 10", nil, true},
		{"No value", "UPDATE test SET a = WHERE age = 10", nil, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			q, err := ParseQuery(test.s)
			if test.errored {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Len(t, q.Statements, 1)
			require.EqualValues(t, test.expected, q.Statements[0])
		})
	}
}
