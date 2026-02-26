package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// OutputWriter handles rendering query results in table, json, or csv format.
type OutputWriter struct {
	format  string
	columns []string
	w       io.Writer
	tw      *tabwriter.Writer
	cw      *csv.Writer
	first   bool
}

// NewOutputWriter creates a new OutputWriter for the given format and columns.
// Supported formats: "table", "json", "csv".
func NewOutputWriter(w io.Writer, format string, columns []string) *OutputWriter {
	ow := &OutputWriter{
		format:  format,
		columns: columns,
		w:       w,
		first:   true,
	}

	switch format {
	case "table":
		ow.tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	case "csv":
		ow.cw = csv.NewWriter(w)
	}

	return ow
}

// WriteHeader writes the column header row.
func (ow *OutputWriter) WriteHeader() {
	switch ow.format {
	case "table":
		fmt.Fprintln(ow.tw, strings.Join(ow.columns, "\t"))
	case "csv":
		ow.cw.Write(ow.columns) //nolint:errcheck
	case "json":
		fmt.Fprint(ow.w, "[")
	}
}

// WriteRow writes a single data row with the given values.
func (ow *OutputWriter) WriteRow(values []string) {
	switch ow.format {
	case "table":
		fmt.Fprintln(ow.tw, strings.Join(values, "\t"))
	case "csv":
		ow.cw.Write(values) //nolint:errcheck
	case "json":
		if !ow.first {
			fmt.Fprint(ow.w, ",")
		}
		ow.first = false
		row := make(map[string]string, len(ow.columns))
		for i, col := range ow.columns {
			if i < len(values) {
				row[col] = values[i]
			}
		}
		data, _ := json.Marshal(row)
		fmt.Fprint(ow.w, string(data))
	}
}

// Flush flushes any buffered output.
func (ow *OutputWriter) Flush() {
	switch ow.format {
	case "table":
		ow.tw.Flush()
	case "csv":
		ow.cw.Flush()
	case "json":
		fmt.Fprintln(ow.w, "]")
	}
}
