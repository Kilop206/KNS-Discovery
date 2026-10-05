package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kns.local/discovery/internal/diff"
	"kns.local/discovery/internal/discovery"
)

const maxHubResponseBytes = 2 << 20

type topologyHubPublisher struct {
	baseURL    *url.URL
	topologyID string
	token      string
	client     *http.Client
}

type hubTopologySummary struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	Version     int64  `json:"version"`
}

type hubTopologyDetail struct {
	Topology hubTopologySummary `json:"topology"`
	Graph    discovery.Snapshot `json:"graph"`
}

func newTopologyHubPublisher(baseURL, topologyID, token string) (*topologyHubPublisher, error) {
	if strings.TrimSpace(topologyID) == "" {
		return nil, fmt.Errorf("hub topology id must not be empty")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("KNS_TOPOLOGY_HUB_TOKEN is required when --hub-topology is configured")
	}

	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("hub-url must be an absolute http or https URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("hub-url must not contain a query or fragment")
	}

	return &topologyHubPublisher{
		baseURL:    parsed,
		topologyID: topologyID,
		token:      token,
		client:     &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (publisher *topologyHubPublisher) endpoint() string {
	base := *publisher.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/api/topologies/" + url.PathEscape(publisher.topologyID)
	return base.String()
}

func (publisher *topologyHubPublisher) request(
	ctx context.Context,
	method string,
	body []byte,
) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, publisher.endpoint(), reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+publisher.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Hub-Request", "1")
	}
	return publisher.client.Do(request)
}

func readHubBody(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxHubResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxHubResponseBytes {
		return nil, fmt.Errorf("Topology Hub response exceeds %d bytes", maxHubResponseBytes)
	}
	return body, nil
}

func hubStatusError(operation string, status int, body []byte) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%s: Topology Hub rejected the desktop token (HTTP %d)", operation, status)
	case http.StatusNotFound:
		return fmt.Errorf("%s: Topology Hub topology was not found", operation)
	case http.StatusConflict:
		return fmt.Errorf("%s: Topology Hub topology changed concurrently; retry after reloading", operation)
	default:
		message := strings.TrimSpace(string(body))
		if len(message) > 240 {
			message = message[:240]
		}
		if message == "" {
			return fmt.Errorf("%s: Topology Hub returned HTTP %d", operation, status)
		}
		return fmt.Errorf("%s: Topology Hub returned HTTP %d: %s", operation, status, message)
	}
}

func (publisher *topologyHubPublisher) current(ctx context.Context) (hubTopologyDetail, error) {
	response, err := publisher.request(ctx, http.MethodGet, nil)
	if err != nil {
		return hubTopologyDetail{}, fmt.Errorf("fetch Hub topology: %w", err)
	}
	body, err := readHubBody(response)
	if err != nil {
		return hubTopologyDetail{}, fmt.Errorf("read Hub topology: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return hubTopologyDetail{}, hubStatusError("fetch Hub topology", response.StatusCode, body)
	}

	var detail hubTopologyDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return hubTopologyDetail{}, fmt.Errorf("decode Hub topology: %w", err)
	}
	if detail.Topology.Version < 0 || detail.Topology.Title == "" ||
		(detail.Topology.Visibility != "PUBLIC" && detail.Topology.Visibility != "PRIVATE") {
		return hubTopologyDetail{}, fmt.Errorf("Topology Hub returned an invalid topology detail")
	}
	if detail.Graph.SchemaVersion != "" && detail.Graph.SchemaVersion != "1.0" {
		return hubTopologyDetail{}, fmt.Errorf("Topology Hub returned unsupported topology schema %q", detail.Graph.SchemaVersion)
	}
	return detail, nil
}

func (publisher *topologyHubPublisher) Publish(ctx context.Context, topology discovery.Snapshot) error {
	current, err := publisher.current(ctx)
	if err != nil {
		return err
	}

	changeSet := diff.Compare(&current.Graph, topology)
	if changeSet.Empty() {
		return nil
	}

	payload := struct {
		Title         string             `json:"title"`
		Description   string             `json:"description"`
		Visibility    string             `json:"visibility"`
		Graph         discovery.Snapshot `json:"graph"`
		Version       int64              `json:"version"`
		DiscoveryDiff diff.Result        `json:"discoveryDiff"`
	}{
		Title:         current.Topology.Title,
		Description:   current.Topology.Description,
		Visibility:    current.Topology.Visibility,
		Graph:         topology,
		Version:       current.Topology.Version,
		DiscoveryDiff: changeSet,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Hub topology update: %w", err)
	}

	response, err := publisher.request(ctx, http.MethodPut, data)
	if err != nil {
		return fmt.Errorf("update Hub topology: %w", err)
	}
	body, err := readHubBody(response)
	if err != nil {
		return fmt.Errorf("read Hub update response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return hubStatusError("update Hub topology", response.StatusCode, body)
	}
	return nil
}
