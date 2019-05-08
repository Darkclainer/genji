package generator

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"io"
	"strconv"
	"strings"
	"unicode"
)

const recordsTmpl = `
{{ define "records" }}
  {{ range .Records }}
    {{ template "record" . }}
  {{ end }}
{{ end }}
`

const recordTmpl = `
{{ define "record" }}
{{ template "record-Field" . }}
{{ template "record-Iterate" . }}
{{ template "record-ScanRecord" . }}
{{ template "record-Pk" . }}
{{ template "store" . }}
{{ template "query-selector" . }}
{{ template "result" . }}
{{ end }}
`

const recordFieldTmpl = `
{{ define "record-Field" }}
{{- $fl := .FirstLetter -}}
{{- $structName := .Name -}}

// Field implements the field method of the record.Record interface.
func ({{$fl}} *{{$structName}}) Field(name string) (field.Field, error) {
	switch name {
	{{- range .Fields }}
	case "{{.Name}}":
		{{- if eq .Type "string"}}
		return field.Field{
			Name: "{{.Name}}",
			Type: field.String,
			Data: []byte({{$fl}}.{{.Name}}),
		}, nil
		{{- else if eq .Type "int64"}}
		return field.Field{
			Name: "{{.Name}}",
			Type: field.Int64,
			Data: field.EncodeInt64({{$fl}}.{{.Name}}),
		}, nil
		{{- end}}
	{{- end}}
	}

	return field.Field{}, errors.New("unknown field")
}
{{ end }}
`

const recordIterateTmpl = `
{{ define "record-Iterate" }}
{{- $fl := .FirstLetter -}}
{{- $structName := .Name -}}

// Iterate through all the fields one by one and pass each of them to the given function.
// It the given function returns an error, the iteration is interrupted.
func ({{$fl}} *{{$structName}}) Iterate(fn func(field.Field) error) error {
	var err error
	var f field.Field

	{{range .Fields}}
	f, _ = {{$fl}}.Field("{{.Name}}")
	err = fn(f)
	if err != nil {
		return err
	}
	{{end}}

	return nil
}
{{ end }}
`

const recordScanRecordTmpl = `
{{ define "record-ScanRecord" }}
{{- $fl := .FirstLetter -}}
{{- $structName := .Name -}}

// ScanRecord extracts fields from record and assigns them to the struct fields.
// It implements the record.Scanner interface.
func ({{$fl}} *{{$structName}}) ScanRecord(rec record.Record) error {
	return rec.Iterate(func(f field.Field) error {
		var err error

		switch f.Name {
		{{- range .Fields}}
		case "{{.Name}}":
			{{- if eq .Type "string"}}
			{{$fl}}.{{.Name}} = string(f.Data)
			{{- else if eq .Type "int64"}}
			{{$fl}}.{{.Name}}, err = field.DecodeInt64(f.Data)
			{{- end}}
		{{- end}}
		}
		return err
	})
}
{{ end }}
`

const recordPkTmpl = `
{{ define "record-Pk" }}
{{- $fl := .FirstLetter -}}
{{- $structName := .Name -}}

{{- if ne .Pk.Name ""}}
// Pk returns the primary key. It implements the table.Pker interface.
func ({{$fl}} *{{$structName}}) Pk() ([]byte, error) {
	{{- if eq .Pk.Type "string"}}
		return []byte({{$fl}}.{{.Pk.Name}}), nil
	{{- else if eq .Pk.Type "int64"}}
		return field.EncodeInt64({{$fl}}.{{.Pk.Name}}), nil
	{{- end}}
}
{{- end}}
{{ end }}
`

type recordContext struct {
	Name   string
	Fields []struct {
		Name, Type string
	}
	Pk struct {
		Name, Type string
	}
}

func (rctx *recordContext) lookupRecord(f *ast.File, target string) (bool, error) {
	for _, n := range f.Decls {
		gn, ok := ast.Node(n).(*ast.GenDecl)
		if !ok || gn.Tok != token.TYPE || len(gn.Specs) == 0 {
			continue
		}

		ts, ok := gn.Specs[0].(*ast.TypeSpec)
		if !ok {
			continue
		}

		if ts.Name.Name != target {
			continue
		}

		s, ok := ts.Type.(*ast.StructType)
		if !ok {
			return false, errors.New("invalid object")
		}

		rctx.Name = target

		for _, fd := range s.Fields.List {
			typ, ok := fd.Type.(*ast.Ident)
			if !ok {
				return false, errors.New("struct must only contain supported fields")
			}

			if len(fd.Names) == 0 {
				return false, errors.New("embedded fields are not supported")
			}

			if typ.Name != "int64" && typ.Name != "string" {
				return false, fmt.Errorf("unsupported type %s", typ.Name)
			}

			for _, name := range fd.Names {
				rctx.Fields = append(rctx.Fields, struct {
					Name, Type string
				}{
					name.String(), string(typ.Name),
				})
			}

			if fd.Tag != nil {
				err := handleGenjiTag(rctx, fd)
				if err != nil {
					return false, err
				}
			}
		}

		return true, nil
	}

	return false, nil
}

func (s *recordContext) IsExported() bool {
	return unicode.IsUpper(rune(s.Name[0]))
}

func (s *recordContext) FirstLetter() string {
	return strings.ToLower(s.Name[0:1])
}

func (s *recordContext) UnexportedName() string {
	if !s.IsExported() {
		return s.Name
	}

	return s.Unexport(s.Name)
}

func (s *recordContext) ExportedName() string {
	if s.IsExported() {
		return s.Name
	}

	return s.Export(s.Name)
}

func (s *recordContext) NameWithPrefix(prefix string) string {
	n := prefix + s.ExportedName()
	if s.IsExported() {
		return s.Export(n)
	}

	return s.Unexport(n)
}

func (s *recordContext) Export(n string) string {
	name := []byte(n)
	name[0] = byte(unicode.ToUpper(rune(n[0])))
	return string(name)
}

func (s *recordContext) Unexport(n string) string {
	name := []byte(n)
	name[0] = byte(unicode.ToLower(rune(n[0])))
	return string(name)
}

// GenerateRecords parses the given asts, looks for the targets structs
// and generates complementary code to the given writer.
func GenerateRecords(w io.Writer, files []*ast.File, targets []string) error {
	if !inSamePackage(files) {
		return errors.New("input files must belong to the same package")
	}

	var buf bytes.Buffer

	fmt.Fprintf(&buf, "package %s\n", files[0].Name.Name)

	fmt.Fprintf(&buf, `
	import (
		"errors"

		"github.com/asdine/genji"
		"github.com/asdine/genji/field"
		"github.com/asdine/genji/query"
		"github.com/asdine/genji/record"
		"github.com/asdine/genji/table"
	)
	`)

	for range targets {
		// ctx, err := lookupRecord(files, target)
		// if err != nil {
		// 	return err
		// }

		// err = t.Execute(&buf, &ctx)
		// if err != nil {
		// 	return err
		// }
	}

	// format using goimports
	output, err := format.Source(buf.Bytes())
	if err != nil {
		return err
	}

	_, err = w.Write(output)
	return err
}

func inSamePackage(files []*ast.File) bool {
	var pkg string

	for _, f := range files {
		if pkg != "" && pkg != f.Name.Name {
			return false
		}
		pkg = f.Name.Name
	}

	return true
}

func handleGenjiTag(ctx *recordContext, fd *ast.Field) error {
	unquoted, err := strconv.Unquote(fd.Tag.Value)
	if err != nil {
		return err
	}

	if !strings.HasPrefix(unquoted, "genji:") {
		return nil
	}

	rawOpts, err := strconv.Unquote(strings.TrimPrefix(unquoted, "genji:"))
	if err != nil {
		return err
	}

	gtags := strings.Split(rawOpts, ",")

	for _, gtag := range gtags {
		switch gtag {
		case "pk":
			if ctx.Pk.Name != "" {
				return errors.New("only one pk field is allowed")
			}

			ctx.Pk.Name = fd.Names[0].Name
			ctx.Pk.Type = fd.Type.(*ast.Ident).Name
		default:
			return fmt.Errorf("unsupported genji tag '%s'", gtag)
		}
	}

	return nil
}
