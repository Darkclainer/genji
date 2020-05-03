package parser

import (
	"strings"
	"testing"
	"time"

	"github.com/asdine/genji/document"
	"github.com/asdine/genji/sql/query/expr"
	"github.com/stretchr/testify/require"
)

func TestParserExpr(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		expected expr.Expr
		fails    bool
	}{
		// integers
		{"+int8", "10", expr.IntValue(10), false},
		{"-int8", "-10", expr.IntValue(-10), false},
		{"+int16", "1000", expr.IntValue(1000), false},
		{"-int16", "-1000", expr.IntValue(-1000), false},
		{"+int32", "10000000", expr.IntValue(10000000), false},
		{"-int32", "-10000000", expr.IntValue(-10000000), false},
		{"+int64", "10000000000", expr.IntValue(10000000000), false},
		{"-int64", "-10000000000", expr.IntValue(-10000000000), false},
		{"> max int64 -> float64", "10000000000000000000", expr.Float64Value(10000000000000000000), false},
		{"< min int64 -> float64", "-10000000000000000000", expr.Float64Value(-10000000000000000000), false},
		{"very large int", "100000000000000000000000000000000000000000000000", expr.Float64Value(100000000000000000000000000000000000000000000000), false},

		// floats
		{"+float64", "10.0", expr.Float64Value(10), false},
		{"-float64", "-10.0", expr.Float64Value(-10), false},

		// durations
		{"+duration", "150ms", expr.DurationValue(150 * time.Millisecond), false},
		{"-duration", "-150ms", expr.DurationValue(-150 * time.Millisecond), false},
		{"bad duration", "-150xs", expr.DurationValue(0), true},

		// strings
		{"double quoted string", `"10.0"`, expr.TextValue("10.0"), false},
		{"single quoted string", "'-10.0'", expr.TextValue("-10.0"), false},

		// identifiers
		{"simple field ref", `a`, expr.FieldSelector{"a"}, false},
		{"simple field ref with quotes", "`some ident`", expr.FieldSelector{"some ident"}, false},
		{"field ref", `a.b.100.c.1.2.3`, expr.FieldSelector{"a", "b", "100", "c", "1", "2", "3"}, false},
		{"field ref negative", `a.b.-100.c`, nil, true},
		{"field ref with spaces", `a.  b.100.  c`, nil, true},
		{"field ref with quotes", "`some ident`.` with`.5.`  quotes`", expr.FieldSelector{"some ident", " with", "5", "  quotes"}, false},

		// documents
		{"empty document", `{}`, expr.KVPairs(nil), false},
		{"document values", `{a: 1, b: 1.0, c: true, d: 'string', e: "string", f: {foo: 'bar'}, g: h.i.j, k: [1, 2, 3]}`,
			expr.KVPairs{
				expr.KVPair{K: "a", V: expr.IntValue(1)},
				expr.KVPair{K: "b", V: expr.Float64Value(1)},
				expr.KVPair{K: "c", V: expr.BoolValue(true)},
				expr.KVPair{K: "d", V: expr.TextValue("string")},
				expr.KVPair{K: "e", V: expr.TextValue("string")},
				expr.KVPair{K: "f", V: expr.KVPairs{
					expr.KVPair{K: "foo", V: expr.TextValue("bar")},
				}},
				expr.KVPair{K: "g", V: expr.FieldSelector([]string{"h", "i", "j"})},
				expr.KVPair{K: "k", V: expr.LiteralExprList{expr.IntValue(1), expr.IntValue(2), expr.IntValue(3)}},
			},
			false},
		{"document keys", `{a: 1, "foo bar __&&))": 1, 'ola ': 1}`,
			expr.KVPairs{
				expr.KVPair{K: "a", V: expr.IntValue(1)},
				expr.KVPair{K: "foo bar __&&))", V: expr.IntValue(1)},
				expr.KVPair{K: "ola ", V: expr.IntValue(1)},
			},
			false},
		{"document keys: same key", `{a: 1, a: 2, "a": 3}`,
			expr.KVPairs{
				expr.KVPair{K: "a", V: expr.IntValue(1)},
				expr.KVPair{K: "a", V: expr.IntValue(2)},
				expr.KVPair{K: "a", V: expr.IntValue(3)},
			},
			false},
		{"bad document keys: param", `{?: 1}`, nil, true},
		{"bad document keys: dot", `{a.b: 1}`, nil, true},
		{"bad document keys: space", `{a b: 1}`, nil, true},
		{"bad document: missing right bracket", `{a: 1`, nil, true},
		{"bad document: missing colon", `{a: 1, 'b'}`, nil, true},

		// list of expressions
		{"list with parentheses: empty", "()", expr.LiteralExprList(nil), false},
		{"list with parentheses: values", `(1, true, {a: 1}, a.b.c, (-1), [-1])`,
			expr.LiteralExprList{
				expr.IntValue(1),
				expr.BoolValue(true),
				expr.KVPairs{expr.KVPair{K: "a", V: expr.IntValue(1)}},
				expr.FieldSelector{"a", "b", "c"},
				expr.LiteralExprList{expr.IntValue(-1)},
				expr.LiteralExprList{expr.IntValue(-1)},
			}, false},
		{"list with parentheses: missing parenthese", `(1, true, {a: 1}, a.b.c, (-1)`, nil, true},
		{"list with brackets: empty", "[]", expr.LiteralExprList(nil), false},
		{"list with brackets: values", `[1, true, {a: 1}, a.b.c, (-1), [-1]]`,
			expr.LiteralExprList{
				expr.IntValue(1),
				expr.BoolValue(true),
				expr.KVPairs{expr.KVPair{K: "a", V: expr.IntValue(1)}},
				expr.FieldSelector{"a", "b", "c"},
				expr.LiteralExprList{expr.IntValue(-1)},
				expr.LiteralExprList{expr.IntValue(-1)},
			}, false},
		{"list with brackets: missing bracket", `[1, true, {a: 1}, a.b.c, (-1), [-1]`, nil, true},

		// operators
		{"=", "age = 10", expr.Eq(expr.FieldSelector([]string{"age"}), expr.IntValue(10)), false},
		{"!=", "age != 10", expr.Neq(expr.FieldSelector([]string{"age"}), expr.IntValue(10)), false},
		{">", "age > 10", expr.Gt(expr.FieldSelector([]string{"age"}), expr.IntValue(10)), false},
		{">=", "age >= 10", expr.Gte(expr.FieldSelector([]string{"age"}), expr.IntValue(10)), false},
		{"<", "age < 10", expr.Lt(expr.FieldSelector([]string{"age"}), expr.IntValue(10)), false},
		{"<=", "age <= 10", expr.Lte(expr.FieldSelector([]string{"age"}), expr.IntValue(10)), false},
		{"+", "age + 10", expr.Add(expr.FieldSelector([]string{"age"}), expr.IntValue(10)), false},
		{"-", "age - 10", expr.Sub(expr.FieldSelector([]string{"age"}), expr.IntValue(10)), false},
		{"*", "age * 10", expr.Mul(expr.FieldSelector([]string{"age"}), expr.IntValue(10)), false},
		{"/", "age / 10", expr.Div(expr.FieldSelector([]string{"age"}), expr.IntValue(10)), false},
		{"%", "age % 10", expr.Mod(expr.FieldSelector([]string{"age"}), expr.IntValue(10)), false},
		{"&", "age & 10", expr.BitwiseAnd(expr.FieldSelector([]string{"age"}), expr.IntValue(10)), false},
		{"IN", "age IN ages", expr.In(expr.FieldSelector([]string{"age"}), expr.FieldSelector([]string{"ages"})), false},
		{"precedence", "4 > 1 + 2", expr.Gt(
			expr.IntValue(4),
			expr.Add(
				expr.IntValue(1),
				expr.IntValue(2),
			),
		), false},
		{"AND", "age = 10 AND age <= 11",
			expr.And(
				expr.Eq(expr.FieldSelector([]string{"age"}), expr.IntValue(10)),
				expr.Lte(expr.FieldSelector([]string{"age"}), expr.IntValue(11)),
			), false},
		{"OR", "age = 10 OR age = 11",
			expr.Or(
				expr.Eq(expr.FieldSelector([]string{"age"}), expr.IntValue(10)),
				expr.Eq(expr.FieldSelector([]string{"age"}), expr.IntValue(11)),
			), false},
		{"AND then OR", "age >= 10 AND age > $age OR age < 10.4",
			expr.Or(
				expr.And(
					expr.Gte(expr.FieldSelector([]string{"age"}), expr.IntValue(10)),
					expr.Gt(expr.FieldSelector([]string{"age"}), expr.NamedParam("age")),
				),
				expr.Lt(expr.FieldSelector([]string{"age"}), expr.Float64Value(10.4)),
			), false},
		{"with NULL", "age > NULL", expr.Gt(expr.FieldSelector([]string{"age"}), expr.NullValue()), false},
		{"pk() function", "pk()", &expr.PKFunc{}, false},
		{"CAST", "CAST(a.b.1.0 AS TEXT)", expr.Cast{Expr: expr.FieldSelector([]string{"a", "b", "1", "0"}), ConvertTo: document.TextValue}, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ex, lit, err := NewParser(strings.NewReader(test.s)).ParseExpr()
			if test.fails {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.EqualValues(t, test.expected, ex)
				require.Equal(t, test.s, lit)
			}
		})
	}
}

func TestParserParams(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		expected expr.Expr
		errored  bool
	}{
		{"one positional", "age = ?", expr.Eq(expr.FieldSelector([]string{"age"}), expr.PositionalParam(1)), false},
		{"multiple positional", "age = ? AND age <= ?",
			expr.And(
				expr.Eq(expr.FieldSelector([]string{"age"}), expr.PositionalParam(1)),
				expr.Lte(expr.FieldSelector([]string{"age"}), expr.PositionalParam(2)),
			), false},
		{"one named", "age = $age", expr.Eq(expr.FieldSelector([]string{"age"}), expr.NamedParam("age")), false},
		{"multiple named", "age = $foo OR age = $bar",
			expr.Or(
				expr.Eq(expr.FieldSelector([]string{"age"}), expr.NamedParam("foo")),
				expr.Eq(expr.FieldSelector([]string{"age"}), expr.NamedParam("bar")),
			), false},
		{"mixed", "age >= ? AND age > $foo OR age < ?", nil, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ex, lit, err := NewParser(strings.NewReader(test.s)).ParseExpr()
			if test.errored {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.EqualValues(t, test.expected, ex)
				require.Equal(t, test.s, lit)
			}
		})
	}
}
