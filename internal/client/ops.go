package client

import (
	"context"
	"strconv"

	"github.com/zoyluo/cronova/internal/model"
)

// Typed convenience wrappers over Call for the operations the CLI renders as
// tables. Everything else (and the MCP server) uses Call generically. Response
// shapes reuse internal/model types — the API returns those directly.

// ListDAGs returns all active DAGs (definition YAML omitted, as the API does).
func (c *Client) ListDAGs(ctx context.Context) ([]model.DAG, error) {
	var out []model.DAG
	_, err := c.CallJSON(ctx, "GET", "/api/dags", Options{}, &out)
	return out, err
}

// GetDAG returns one DAG definition (with tasks).
func (c *Client) GetDAG(ctx context.Context, id string) (*model.DAG, error) {
	var out model.DAG
	_, err := c.CallJSON(ctx, "GET", "/api/dags/{id}", Options{Path: map[string]string{"id": id}}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListRuns returns a DAG's recent runs, newest first.
func (c *Client) ListRuns(ctx context.Context, dagID string, limit int) ([]model.DagRun, error) {
	q := map[string]string{}
	if limit > 0 {
		q["limit"] = strconv.Itoa(limit)
	}
	var out []model.DagRun
	_, err := c.CallJSON(ctx, "GET", "/api/dags/{id}/runs", Options{Path: map[string]string{"id": dagID}, Query: q}, &out)
	return out, err
}

// ListPools returns concurrency pools.
func (c *Client) ListPools(ctx context.Context) ([]model.Pool, error) {
	var out []model.Pool
	_, err := c.CallJSON(ctx, "GET", "/api/pools", Options{}, &out)
	return out, err
}

// TriggerDAG starts a manual run and returns its run id.
func (c *Client) TriggerDAG(ctx context.Context, dagID string, params map[string]string) (string, error) {
	var body []byte
	if len(params) > 0 {
		b, err := jsonBody(map[string]any{"params": params})
		if err != nil {
			return "", err
		}
		body = b
	}
	var out struct {
		RunID string `json:"run_id"`
	}
	_, err := c.CallJSON(ctx, "POST", "/api/dags/{id}/trigger", Options{Path: map[string]string{"id": dagID}, Body: body}, &out)
	return out.RunID, err
}

// TaskLog returns the bounded tail of a task instance's captured log.
func (c *Client) TaskLog(ctx context.Context, tiID string) (string, error) {
	res, err := c.Call(ctx, "GET", "/api/tasks/{tiID}/log", Options{Path: map[string]string{"tiID": tiID}, Accept: "text/plain"})
	if err != nil {
		return "", err
	}
	return string(res.Body), nil
}

// Ping checks reachability + auth by calling /api/me (401 if the token is bad).
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Call(ctx, "GET", "/api/me", Options{})
	return err
}
