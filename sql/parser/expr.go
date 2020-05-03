package parser

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/asdine/genji/document"
	"github.com/asdine/genji/sql/query/expr"
	"github.com/asdine/genji/sql/scanner"
)

type operator interface {
	Precedence() int
	LeftHand() expr.Expr
	RightHand() expr.Expr
	SetLeftHandExpr(expr.Expr)
	SetRightHandExpr(expr.Expr)
}

// ParseExpr parses an expression.
func (p *Parser) ParseExpr() (e expr.Expr, lit string, err error) {
	// enable the expression buffer to store the literal representation
	// of the parsed expression
	if p.buf == nil {
		p.buf = new(bytes.Buffer)
		defer func() { p.buf = nil }()
	}

	// Dummy root node.
	var root operator = expr.NewCmpOp(nil, nil, 0)

	// Parse a non-binary expression type to start.
	// This variable will always be the root of the expression tree.
	e, err = p.parseUnaryExpr()
	if err != nil {
		return nil, "", err
	}
	root.SetRightHandExpr(e)

	// Loop over operations and unary exprs and build a tree based on precedence.
	for {
		// If the next token is NOT an operator then return the expression.
		op, _, _ := p.ScanIgnoreWhitespace()
		if !op.IsOperator() {
			p.Unscan()
			return root.RightHand(), strings.TrimSpace(p.buf.String()), nil
		}

		var rhs expr.Expr

		if rhs, err = p.parseUnaryExpr(); err != nil {
			return nil, "", err
		}

		// Find the right spot in the tree to add the new expression by
		// descending the RHS of the expression tree until we reach the last
		// BinaryExpr or a BinaryExpr whose RHS has an operator with
		// precedence >= the operator being added.
		for node := root.(operator); ; {
			p, ok := node.RightHand().(operator)
			if !ok || p.Precedence() >= op.Precedence() {
				// Add the new expression here and break.
				node.SetRightHandExpr(opToExpr(op, node.RightHand(), rhs))
				break
			}
			node = p
		}
	}
}

func opToExpr(op scanner.Token, lhs, rhs expr.Expr) expr.Expr {
	switch op {
	case scanner.EQ:
		return expr.Eq(lhs, rhs)
	case scanner.NEQ:
		return expr.Neq(lhs, rhs)
	case scanner.GT:
		return expr.Gt(lhs, rhs)
	case scanner.GTE:
		return expr.Gte(lhs, rhs)
	case scanner.LT:
		return expr.Lt(lhs, rhs)
	case scanner.LTE:
		return expr.Lte(lhs, rhs)
	case scanner.AND:
		return expr.And(lhs, rhs)
	case scanner.OR:
		return expr.Or(lhs, rhs)
	case scanner.ADD:
		return expr.Add(lhs, rhs)
	case scanner.SUB:
		return expr.Sub(lhs, rhs)
	case scanner.MUL:
		return expr.Mul(lhs, rhs)
	case scanner.DIV:
		return expr.Div(lhs, rhs)
	case scanner.MOD:
		return expr.Mod(lhs, rhs)
	case scanner.BITWISEAND:
		return expr.BitwiseAnd(lhs, rhs)
	case scanner.BITWISEOR:
		return expr.BitwiseOr(lhs, rhs)
	case scanner.BITWISEXOR:
		return expr.BitwiseXor(lhs, rhs)
	case scanner.IN:
		return expr.In(lhs, rhs)
	case scanner.IS:
		return expr.Is(lhs, rhs)
	}

	panic(fmt.Sprintf("unknown operator %q", op))
}

// parseUnaryExpr parses an non-binary expression.
func (p *Parser) parseUnaryExpr() (expr.Expr, error) {
	tok, pos, lit := p.ScanIgnoreWhitespace()
	switch tok {
	case scanner.CAST:
		p.Unscan()
		return p.parseCastExpression()
	case scanner.IDENT:
		// if the next token is a left parenthesis, this is a function
		if tok1, _, _ := p.Scan(); tok1 == scanner.LPAREN {
			p.Unscan()
			p.Unscan()
			return p.parseFunction()
		}
		p.Unscan()
		p.Unscan()
		field, err := p.parseFieldRef()
		if err != nil {
			return nil, err
		}
		fs := expr.FieldSelector(field)
		return fs, nil
	case scanner.NAMEDPARAM:
		if len(lit) == 1 {
			return nil, &ParseError{Message: "missing param name"}
		}
		if p.orderedParams > 0 {
			return nil, &ParseError{Message: "can't mix positional arguments with named arguments"}
		}
		p.namedParams++
		return expr.NamedParam(lit[1:]), nil
	case scanner.POSITIONALPARAM:
		if p.namedParams > 0 {
			return nil, &ParseError{Message: "can't mix positional arguments with named arguments"}
		}
		p.orderedParams++
		return expr.PositionalParam(p.orderedParams), nil
	case scanner.STRING:
		return expr.TextValue(lit), nil
	case scanner.NUMBER:
		v, err := strconv.ParseFloat(lit, 64)
		if err != nil {
			return nil, &ParseError{Message: "unable to parse number", Pos: pos}
		}
		return expr.Float64Value(v), nil
	case scanner.INTEGER:
		v, err := strconv.Atoi(lit)
		if err != nil {
			// The literal may be too large to fit into an int64, parse as Float64
			if v, err := strconv.ParseFloat(lit, 64); err == nil {
				return expr.Float64Value(v), nil
			}
			return nil, &ParseError{Message: "unable to parse integer", Pos: pos}
		}
		return expr.IntValue(int(v)), nil
	case scanner.TRUE, scanner.FALSE:
		return expr.BoolValue(tok == scanner.TRUE), nil
	case scanner.NULL:
		return expr.NullValue(), nil
	case scanner.DURATION:
		d, err := time.ParseDuration(lit)
		if err != nil {
			return nil, &ParseError{Message: "unable to parse duration", Pos: pos}
		}
		return expr.DurationValue(d), nil
	case scanner.LBRACKET:
		p.Unscan()
		e, _, err := p.parseDocument()
		return e, err
	case scanner.LSBRACKET:
		p.Unscan()
		return p.parseExprList(scanner.LSBRACKET, scanner.RSBRACKET)
	case scanner.LPAREN:
		p.Unscan()
		return p.parseExprList(scanner.LPAREN, scanner.RPAREN)
	default:
		return nil, newParseError(scanner.Tokstr(tok, lit), []string{"identifier", "string", "number", "bool"}, pos)
	}
}

// parseIdent parses an identifier.
func (p *Parser) parseIdent() (string, error) {
	tok, pos, lit := p.ScanIgnoreWhitespace()
	if tok != scanner.IDENT {
		return "", newParseError(scanner.Tokstr(tok, lit), []string{"identifier"}, pos)
	}

	return lit, nil
}

// parseIdentList parses a comma delimited list of identifiers.
func (p *Parser) parseIdentList() ([]string, error) {
	// Parse first (required) identifier.
	ident, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	idents := []string{ident}

	// Parse remaining (optional) identifiers.
	for {
		if tok, _, _ := p.ScanIgnoreWhitespace(); tok != scanner.COMMA {
			p.Unscan()
			return idents, nil
		}

		if ident, err = p.parseIdent(); err != nil {
			return nil, err
		}

		idents = append(idents, ident)
	}
}

// parseParam parses a positional or named param.
func (p *Parser) parseParam() (expr.Expr, error) {
	tok, _, lit := p.ScanIgnoreWhitespace()
	switch tok {
	case scanner.NAMEDPARAM:
		if len(lit) == 1 {
			return nil, &ParseError{Message: "missing param name"}
		}
		if p.orderedParams > 0 {
			return nil, &ParseError{Message: "can't mix positional arguments with named arguments"}
		}
		p.namedParams++
		return expr.NamedParam(lit[1:]), nil
	case scanner.POSITIONALPARAM:
		if p.namedParams > 0 {
			return nil, &ParseError{Message: "can't mix positional arguments with named arguments"}
		}
		p.orderedParams++
		return expr.PositionalParam(p.orderedParams), nil
	default:
		return nil, nil
	}
}

func (p *Parser) parseType() document.ValueType {
	tok, _, _ := p.ScanIgnoreWhitespace()
	switch tok {
	case scanner.TYPEBYTES:
		return document.BlobValue
	case scanner.TYPESTRING:
		return document.TextValue
	case scanner.TYPEBOOL:
		return document.BoolValue
	case scanner.TYPEINT8:
		return document.Int8Value
	case scanner.TYPEINT16:
		return document.Int16Value
	case scanner.TYPEINT32:
		return document.Int32Value
	case scanner.TYPEINT64, scanner.TYPEINT, scanner.TYPEINTEGER:
		return document.Int64Value
	case scanner.TYPEFLOAT64, scanner.TYPENUMERIC:
		return document.Float64Value
	case scanner.TYPETEXT:
		return document.TextValue
	case scanner.TYPEDURATION:
		return document.DurationValue
	}

	p.Unscan()
	return 0
}

// parseDocument parses a document
func (p *Parser) parseDocument() (expr.Expr, bool, error) {
	// Parse { token.
	if tok, _, _ := p.ScanIgnoreWhitespace(); tok != scanner.LBRACKET {
		p.Unscan()
		return nil, false, nil
	}

	var pairs expr.KVPairs
	var pair expr.KVPair
	var err error

	// Parse kv pairs.
	for {
		if pair, err = p.parseKV(); err != nil {
			p.Unscan()
			break
		}

		pairs = append(pairs, pair)

		if tok, _, _ := p.ScanIgnoreWhitespace(); tok != scanner.COMMA {
			p.Unscan()
			break
		}
	}

	// Parse required } token.
	if tok, pos, lit := p.ScanIgnoreWhitespace(); tok != scanner.RBRACKET {
		return nil, true, newParseError(scanner.Tokstr(tok, lit), []string{"}"}, pos)
	}

	return pairs, true, nil
}

// parseKV parses a key-value pair in the form IDENT : Expr.
func (p *Parser) parseKV() (expr.KVPair, error) {
	var k string

	tok, pos, lit := p.ScanIgnoreWhitespace()
	if tok == scanner.IDENT || tok == scanner.STRING {
		k = lit
	} else {
		return expr.KVPair{}, newParseError(scanner.Tokstr(tok, lit), []string{"ident", "string"}, pos)
	}

	tok, pos, lit = p.ScanIgnoreWhitespace()
	if tok != scanner.COLON {
		p.Unscan()
		return expr.KVPair{}, newParseError(scanner.Tokstr(tok, lit), []string{":"}, pos)
	}

	e, _, err := p.ParseExpr()
	if err != nil {
		return expr.KVPair{}, err
	}

	return expr.KVPair{
		K: k,
		V: e,
	}, nil
}

// parseFieldRef parses a field reference in the form ident (.ident|integer)*
func (p *Parser) parseFieldRef() ([]string, error) {
	var fieldRef []string
	// parse first mandatory ident
	chunk, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	fieldRef = append(fieldRef, chunk)

LOOP:
	for {
		// scan the very next token.
		// if can be either a '.' or a number starting with '.'
		// because the scanner is unable to detect a dot when
		// it's followed by digits.
		// Otherwise, unscan and return the fieldRef
		tok, _, lit := p.Scan()
		switch tok {
		case scanner.DOT:
			// scan the next token for an ident
			tok, pos, lit := p.Scan()
			if tok != scanner.IDENT {
				return nil, newParseError(lit, []string{"array index", "identifier"}, pos)
			}
			fieldRef = append(fieldRef, lit)
		case scanner.NUMBER:
			// if it starts with a dot, it's considered as an array index
			if lit[0] != '.' {
				p.Unscan()
				return fieldRef, nil
			}
			lit = lit[1:]
			fieldRef = append(fieldRef, lit)
		default:
			p.Unscan()
			break LOOP
		}
	}

	return fieldRef, nil
}

func (p *Parser) parseExprList(leftToken, rightToken scanner.Token) (expr.LiteralExprList, error) {
	// Parse ( or [ token.
	if tok, pos, lit := p.ScanIgnoreWhitespace(); tok != leftToken {
		return nil, newParseError(scanner.Tokstr(tok, lit), []string{leftToken.String()}, pos)
	}

	var exprList expr.LiteralExprList
	var expr expr.Expr
	var err error

	// Parse expressions.
	for {
		if expr, _, err = p.ParseExpr(); err != nil {
			p.Unscan()
			break
		}

		exprList = append(exprList, expr)

		if tok, _, _ := p.ScanIgnoreWhitespace(); tok != scanner.COMMA {
			p.Unscan()
			break
		}
	}

	// Parse required ) or ] token.
	if tok, pos, lit := p.ScanIgnoreWhitespace(); tok != rightToken {
		return nil, newParseError(scanner.Tokstr(tok, lit), []string{rightToken.String()}, pos)
	}

	return exprList, nil
}

// parseFunction parses a function call.
// a function is an identifier followed by a parenthesis,
// an optional coma-separated list of expressions and a closing parenthesis.
func (p *Parser) parseFunction() (expr.Expr, error) {
	// Parse function name.
	fname, err := p.parseIdent()
	if err != nil {
		return nil, err
	}

	// Parse required ( token.
	if tok, pos, lit := p.ScanIgnoreWhitespace(); tok != scanner.LPAREN {
		return nil, newParseError(scanner.Tokstr(tok, lit), []string{"("}, pos)
	}

	// Check if the function is called without arguments.
	if tok, _, _ := p.ScanIgnoreWhitespace(); tok == scanner.RPAREN {
		return expr.GetFunc(fname)
	}
	p.Unscan()

	var exprs []expr.Expr

	// Parse expressions.
	for {
		e, _, err := p.ParseExpr()
		if err != nil {
			return nil, err
		}

		exprs = append(exprs, e)

		if tok, _, _ := p.ScanIgnoreWhitespace(); tok != scanner.COMMA {
			p.Unscan()
			return expr.GetFunc(fname, exprs...)
		}
	}
}

// parseCastExpression parses a string of the form CAST(expr AS type).
func (p *Parser) parseCastExpression() (expr.Expr, error) {
	// Parse required CAST token.
	if tok, pos, lit := p.ScanIgnoreWhitespace(); tok != scanner.CAST {
		return nil, newParseError(scanner.Tokstr(tok, lit), []string{"CAST"}, pos)
	}

	// Parse required ( token.
	if tok, pos, lit := p.ScanIgnoreWhitespace(); tok != scanner.LPAREN {
		return nil, newParseError(scanner.Tokstr(tok, lit), []string{"("}, pos)
	}

	// parse required expression.
	e, _, err := p.ParseExpr()
	if err != nil {
		return nil, err
	}

	// Parse required AS token.
	if tok, pos, lit := p.ScanIgnoreWhitespace(); tok != scanner.AS {
		return nil, newParseError(scanner.Tokstr(tok, lit), []string{"AS"}, pos)
	}

	// Parse require typename.
	tp := p.parseType()
	if tp == 0 {
		tok, pos, lit := p.ScanIgnoreWhitespace()
		p.Unscan()
		return nil, newParseError(scanner.Tokstr(tok, lit), []string{"type"}, pos)
	}

	// Parse required ) token.
	if tok, pos, lit := p.ScanIgnoreWhitespace(); tok != scanner.RPAREN {
		return nil, newParseError(scanner.Tokstr(tok, lit), []string{")"}, pos)
	}

	return expr.Cast{Expr: e, ConvertTo: tp}, nil
}
