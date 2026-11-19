package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/costreport"
)

const (
	pageSize = 500     // the admin API maximum
	maxPages = 100_000 // bounds a runaway cursor
	maxBody  = 64 << 20
)

// adminCalls reads the call log from the admin API, page by page.
type adminCalls struct {
	base string
	http *http.Client
}

type callPage struct {
	Calls []struct {
		ID           string `json:"id"`
		TS           string `json:"ts"`
		Key          string `json:"key"`
		CostMicroUSD *int64 `json:"cost_micro_usd"`
	} `json:"calls"`
	NextCursor *string `json:"next_cursor"`
}

// Calls returns every call in [from, to).
func (a adminCalls) Calls(ctx context.Context, from, to time.Time) ([]costreport.Call, error) {
	var out []costreport.Call
	cursor := ""
	for page := 0; page < maxPages; page++ {
		q := url.Values{
			"from":  {from.UTC().Format(time.RFC3339)},
			"to":    {to.UTC().Format(time.RFC3339)},
			"limit": {strconv.Itoa(pageSize)},
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var p callPage
		if err := a.get(ctx, "/admin/calls?"+q.Encode(), &p); err != nil {
			return nil, err
		}
		for _, c := range p.Calls {
			ts, err := time.Parse(time.RFC3339Nano, c.TS)
			if err != nil {
				return nil, fmt.Errorf("admin API call %s has ts %q, which is not RFC 3339", c.ID, c.TS)
			}
			call := costreport.Call{ID: c.ID, TS: ts.UTC(), Key: c.Key}
			if c.CostMicroUSD != nil {
				m := core.MicroUSD(*c.CostMicroUSD)
				call.Cost = &m
			}
			out = append(out, call)
		}
		if p.NextCursor == nil || *p.NextCursor == "" {
			return out, nil
		}
		cursor = *p.NextCursor
	}
	return nil, fmt.Errorf("admin API call list did not end after %d pages", maxPages)
}

func (a adminCalls) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the admin API at %s (set SPYGLASS_ADMIN_URL or --admin-url): %w", a.base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("read admin API response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("admin API answered %d for %s", resp.StatusCode, a.base+"/admin/calls")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("admin API answered with a body that is not the call list: %w", err)
	}
	return nil
}
