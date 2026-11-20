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
const maxHubRequestBytes = 1 << 20

type topologyStorePublisher struct {
	baseURL    *url.URL
	topologyID string
	token      string
	client     *http.Client
}

type storedTopologySummary struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	Version     int64  `json:"version"`
}

type storedTopologyDetail struct {
	Topology storedTopologySummary `json:"topology"`
	Graph    discovery.Snapshot    `json:"graph"`
}

// Keep this contract aligned with the desktop KNS client and Hub route.  The
// identifier is part of a URL path segment, so accepting separators or escape
// sequences here would produce a request for a different topology.
func validHubTopologyID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_') {
			return false
		}
	}
	return true
}

func newTopologyStorePublisher(baseURL, topologyID, token string) (*topologyStorePublisher, error) {
	if !validHubTopologyID(topologyID) {
		return nil, fmt.Errorf("hub topology id must contain 1 to 128 letters, digits, hyphens, or underscores")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("KNS_TOPOLOGY_STORE_TOKEN is required when --hub-topology is configured")
	}

	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("hub-url must be an absolute http or https URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return nil, fmt.Errorf("hub-url must not contain credentials, a query or fragment")
	}

	return &topologyStorePublisher{
		baseURL:    parsed,
		topologyID: topologyID,
		token:      token,
		client: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (publisher *topologyStorePublisher) endpoint() string {
	base := *publisher.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/api/topologies/" + publisher.topologyID
	return base.String()
}

func (publisher *topologyStorePublisher) request(
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
		return nil, fmt.Errorf("topology store response exceeds %d bytes", maxHubResponseBytes)
	}
	return body, nil
}

func hubStatusError(operation string, status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%s: topology store rejected the desktop token (HTTP %d)", operation, status)
	case http.StatusNotFound:
		return fmt.Errorf("%s: topology store topology was not found", operation)
	case http.StatusConflict:
		return fmt.Errorf("%s: topology store topology changed concurrently; retry after reloading", operation)
	default:
		return fmt.Errorf("%s: topology store returned HTTP %d", operation, status)
	}
}

func (publisher *topologyStorePublisher) current(ctx context.Context) (storedTopologyDetail, error) {
	response, err := publisher.request(ctx, http.MethodGet, nil)
	if err != nil {
		return storedTopologyDetail{}, fmt.Errorf("fetch Hub topology: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return storedTopologyDetail{}, hubStatusError("fetch Hub topology", response.StatusCode)
	}
	body, err := readHubBody(response)
	if err != nil {
		return storedTopologyDetail{}, fmt.Errorf("read Hub topology: %w", err)
	}

	var detail storedTopologyDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return storedTopologyDetail{}, fmt.Errorf("decode Hub topology: %w", err)
	}
	if detail.Topology.Version < 0 || detail.Topology.Title == "" ||
		(detail.Topology.Visibility != "PUBLIC" && detail.Topology.Visibility != "PRIVATE") {
		return storedTopologyDetail{}, fmt.Errorf("topology store returned an invalid topology detail")
	}
	if detail.Graph.SchemaVersion != "" && detail.Graph.SchemaVersion != "1.0" {
		return storedTopologyDetail{}, fmt.Errorf("topology store returned unsupported topology schema %q", detail.Graph.SchemaVersion)
	}
	return detail, nil
}

func (publisher *topologyStorePublisher) Publish(ctx context.Context, topology discovery.Snapshot) error {
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
	if len(data) > maxHubRequestBytes {
		return fmt.Errorf("topology store update exceeds 1 MiB including revision metadata and discovery diff")
	}

	response, err := publisher.request(ctx, http.MethodPut, data)
	if err != nil {
		return fmt.Errorf("update Hub topology: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return hubStatusError("update Hub topology", response.StatusCode)
	}
	_, err = readHubBody(response)
	if err != nil {
		return fmt.Errorf("read Hub update response: %w", err)
	}
	return nil
}
