// Package taskfile is the TaskSource adapter for a plain task list in a CSV or
// JSON file, for people who do not run firstmate.
//
// Both formats carry the same fields:
//
//	id         required, the task id
//	start      required, RFC 3339: the first instant of the task window
//	end        optional, RFC 3339: the first instant after it; empty means open
//	merged_pr  optional, the merged pull request, for example its URL
//	key        optional, the virtual key name of the task's calls; default id
//
// CSV has a header row naming the columns, in any order. JSON is an array of
// objects with these names, or an object with that array under "tasks".
package taskfile

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mtanzi/spyglass/internal/costreport"
)

const (
	maxFile = 16 << 20
	maxText = 512 // longest id, key or PR kept
)

// File reads a task file. The format follows the extension: .csv or .json.
type File struct{ Path string }

// Tasks reads and parses the file.
func (f File) Tasks(ctx context.Context) (costreport.TaskList, error) {
	var parse func(io.Reader) (costreport.TaskList, error)
	switch strings.ToLower(filepath.Ext(f.Path)) {
	case ".csv":
		parse = ParseCSV
	case ".json":
		parse = ParseJSON
	default:
		return costreport.TaskList{}, fmt.Errorf("task file %q: the extension must be .csv or .json", f.Path)
	}
	fh, err := os.Open(f.Path)
	if err != nil {
		return costreport.TaskList{}, fmt.Errorf("open task file: %w", err)
	}
	defer func() { _ = fh.Close() }()
	if err := ctx.Err(); err != nil {
		return costreport.TaskList{}, err
	}
	return parse(io.LimitReader(fh, maxFile+1))
}

// row is one task as written, before validation.
type row struct {
	ID       string `json:"id"`
	Start    string `json:"start"`
	End      string `json:"end"`
	MergedPR string `json:"merged_pr"`
	Key      string `json:"key"`
}

func (r row) task() (costreport.Task, error) {
	r = row{strings.TrimSpace(r.ID), strings.TrimSpace(r.Start), strings.TrimSpace(r.End),
		strings.TrimSpace(r.MergedPR), strings.TrimSpace(r.Key)}
	switch {
	case r.ID == "":
		return costreport.Task{}, errors.New("no id")
	case len(r.ID) > maxText || len(r.Key) > maxText || len(r.MergedPR) > maxText:
		return costreport.Task{}, errors.New("a field is longer than 512 bytes")
	}
	start, err := time.Parse(time.RFC3339, r.Start)
	if err != nil {
		return costreport.Task{}, fmt.Errorf("task %q: start is not an RFC 3339 time", r.ID)
	}
	t := costreport.Task{ID: r.ID, Key: r.Key, Start: start.UTC(), MergedPR: r.MergedPR}
	if t.Key == "" {
		t.Key = r.ID
	}
	if r.End != "" {
		end, err := time.Parse(time.RFC3339, r.End)
		if err != nil {
			return costreport.Task{}, fmt.Errorf("task %q: end is not an RFC 3339 time", r.ID)
		}
		if !end.After(start) {
			return costreport.Task{}, fmt.Errorf("task %q: end is not after start", r.ID)
		}
		t.End = end.UTC()
	}
	return t, nil
}

// ParseCSV reads CSV with a header row. An unusable row is skipped and named
// in Warnings. A missing id or start column is an error.
func ParseCSV(r io.Reader) (costreport.TaskList, error) {
	data, err := readAll(r)
	if err != nil {
		return costreport.TaskList{}, err
	}
	cr := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	head, err := cr.Read()
	if err != nil {
		return costreport.TaskList{}, fmt.Errorf("task CSV: no header row: %w", err)
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, need := range []string{"id", "start"} {
		if _, ok := col[need]; !ok {
			return costreport.TaskList{}, fmt.Errorf("task CSV: the header has no %q column", need)
		}
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return rec[i]
		}
		return ""
	}
	var w costreport.Warner
	var out costreport.TaskList
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			line, _ := cr.FieldPos(0)
			w.Addf("line %d: not valid CSV, skipped", line)
			continue
		}
		line, _ := cr.FieldPos(0)
		t, err := row{get(rec, "id"), get(rec, "start"), get(rec, "end"), get(rec, "merged_pr"), get(rec, "key")}.task()
		if err != nil {
			w.Addf("line %d: %v, skipped", line, err)
			continue
		}
		out.Tasks = append(out.Tasks, t)
	}
	out.Warnings = w.List()
	return out, nil
}

// ParseJSON reads an array of task objects, or an object holding the array
// under "tasks". An unusable element is skipped and named in Warnings.
func ParseJSON(r io.Reader) (costreport.TaskList, error) {
	data, err := readAll(r)
	if err != nil {
		return costreport.TaskList{}, err
	}
	var raws []json.RawMessage
	data = bytes.TrimSpace(data)
	if bytes.HasPrefix(data, []byte("{")) {
		var wrap struct {
			Tasks []json.RawMessage `json:"tasks"`
		}
		if err := json.Unmarshal(data, &wrap); err != nil {
			return costreport.TaskList{}, fmt.Errorf("task JSON: %w", err)
		}
		raws = wrap.Tasks
	} else if err := json.Unmarshal(data, &raws); err != nil {
		return costreport.TaskList{}, fmt.Errorf("task JSON: want an array of task objects: %w", err)
	}
	var w costreport.Warner
	var out costreport.TaskList
	for i, raw := range raws {
		var rw row
		if err := json.Unmarshal(raw, &rw); err != nil {
			w.Addf("task %d: not an object with string fields, skipped", i+1)
			continue
		}
		t, err := rw.task()
		if err != nil {
			w.Addf("task %d: %v, skipped", i+1, err)
			continue
		}
		out.Tasks = append(out.Tasks, t)
	}
	out.Warnings = w.List()
	return out, nil
}

func readAll(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read task file: %w", err)
	}
	if len(data) > maxFile {
		return nil, fmt.Errorf("task file is larger than %d MiB", maxFile>>20)
	}
	return data, nil
}
