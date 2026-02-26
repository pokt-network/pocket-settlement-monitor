package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputWriter_TableFormat(t *testing.T) {
	var buf bytes.Buffer
	ow := NewOutputWriter(&buf, "table", []string{"NAME", "VALUE"})
	ow.WriteHeader()
	ow.WriteRow([]string{"foo", "123"})
	ow.WriteRow([]string{"bar", "456"})
	ow.Flush()

	output := buf.String()
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "VALUE")
	assert.Contains(t, output, "foo")
	assert.Contains(t, output, "123")
	assert.Contains(t, output, "bar")
	assert.Contains(t, output, "456")
}

func TestOutputWriter_CSVFormat(t *testing.T) {
	var buf bytes.Buffer
	ow := NewOutputWriter(&buf, "csv", []string{"NAME", "VALUE"})
	ow.WriteHeader()
	ow.WriteRow([]string{"foo", "123"})
	ow.WriteRow([]string{"bar", "456"})
	ow.Flush()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "NAME,VALUE", lines[0])
	assert.Equal(t, "foo,123", lines[1])
	assert.Equal(t, "bar,456", lines[2])
}

func TestOutputWriter_CSVSpecialChars(t *testing.T) {
	var buf bytes.Buffer
	ow := NewOutputWriter(&buf, "csv", []string{"NAME", "VALUE"})
	ow.WriteHeader()
	ow.WriteRow([]string{"foo,bar", `he said "hi"`})
	ow.Flush()

	output := buf.String()
	assert.Contains(t, output, `"foo,bar"`)
	assert.Contains(t, output, `"he said ""hi"""`)
}

func TestOutputWriter_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	ow := NewOutputWriter(&buf, "json", []string{"name", "value"})
	ow.WriteHeader()
	ow.WriteRow([]string{"foo", "123"})
	ow.WriteRow([]string{"bar", "456"})
	ow.Flush()

	var result []map[string]string
	err := json.Unmarshal(buf.Bytes(), &result)
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "foo", result[0]["name"])
	assert.Equal(t, "123", result[0]["value"])
	assert.Equal(t, "bar", result[1]["name"])
	assert.Equal(t, "456", result[1]["value"])
}

func TestOutputWriter_JSONSingleRow(t *testing.T) {
	var buf bytes.Buffer
	ow := NewOutputWriter(&buf, "json", []string{"id"})
	ow.WriteHeader()
	ow.WriteRow([]string{"42"})
	ow.Flush()

	var result []map[string]string
	err := json.Unmarshal(buf.Bytes(), &result)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "42", result[0]["id"])
}

func TestOutputWriter_JSONEmptyResult(t *testing.T) {
	var buf bytes.Buffer
	ow := NewOutputWriter(&buf, "json", []string{"id"})
	ow.WriteHeader()
	ow.Flush()

	var result []map[string]string
	err := json.Unmarshal(buf.Bytes(), &result)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestOutputWriter_MoreValuesThanColumns(t *testing.T) {
	var buf bytes.Buffer
	ow := NewOutputWriter(&buf, "json", []string{"name"})
	ow.WriteHeader()
	ow.WriteRow([]string{"foo", "extra1", "extra2"})
	ow.Flush()

	var result []map[string]string
	err := json.Unmarshal(buf.Bytes(), &result)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "foo", result[0]["name"])
	assert.Len(t, result[0], 1, "extra values beyond columns should not appear")
}

func TestOutputWriter_FewerValuesThanColumns(t *testing.T) {
	var buf bytes.Buffer
	ow := NewOutputWriter(&buf, "json", []string{"name", "value", "extra"})
	ow.WriteHeader()
	ow.WriteRow([]string{"foo"})
	ow.Flush()

	var result []map[string]string
	err := json.Unmarshal(buf.Bytes(), &result)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "foo", result[0]["name"])
	_, hasValue := result[0]["value"]
	assert.False(t, hasValue, "missing values should not produce keys")
}
