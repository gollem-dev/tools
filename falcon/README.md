# falcon

A [gollem](https://github.com/gollem-dev/gollem) `ToolSet` for **read-only**
CrowdStrike Falcon queries: incidents, alerts, behaviors, devices, CrowdScores,
and raw EDR telemetry events (Next-Gen SIEM). It does not modify any data in your
Falcon environment.

## Why in-memory pagination

Falcon result sets — especially alert details and EDR events — can be far larger
than an LLM's context can usefully hold. To bound this, every search tool:

- returns at most `maxRecords` (default **100**) records per call,
- fetches and keeps up to `maxFetchRecords` (default **2000**) records of the
  match set in memory, and
- returns an opaque `page_token` when more results remain.

The LLM fetches the next page by calling the **same tool** again with
`page_token`; cached pages are served from memory **without** another Falcon
request. When the match set exceeds 2000, the total count is reported as
`total_available` where the API provides it (incidents, behaviors, devices,
alerts); for events it is reported via `truncated` + a `note`.

> The page store keeps cross-call state in process memory, scoped to a single
> `ToolSet` instance, guarded by a mutex, and evicted by an LRU count cap and a
> TTL (`WithPageTTL`, default 10m). Use one `ToolSet` per agent session rather
> than sharing a single instance across independent concurrent agents.

## Usage

```go
ts, err := falcon.New("<CLIENT_ID>", "<CLIENT_SECRET>",
    // falcon.WithBaseURL("https://api.us-2.crowdstrike.com"), // non-US-1 region
    // falcon.WithMaxRecords(50),
    // falcon.WithMaxFetchRecords(1000),
)
if err != nil {
    return err
}
if err := ts.Ping(ctx); err != nil { // optional credential preflight
    return err
}
agent := gollem.New(llm, gollem.WithToolSets(ts))
```

`New` only validates configuration and builds in-memory state; it performs no
network I/O. `Ping` verifies credentials by acquiring an OAuth2 token.

### Options

| Option | Default | Purpose |
|---|---|---|
| `WithBaseURL(string)` | `https://api.crowdstrike.com` (US-1) | Cloud-region API base URL |
| `WithHTTPClient(*http.Client)` | 60s-timeout client | Custom HTTP client (also used for tokens) |
| `WithLogger(*slog.Logger)` | `slog.Default()` | Structured logger |
| `WithMaxRecords(int)` | `100` | Records returned per page |
| `WithMaxFetchRecords(int)` | `2000` | Records held in memory per search |
| `WithPageTTL(time.Duration)` | `10m` | `page_token` validity |

## Cloud regions

Set the base URL matching your Falcon tenant region (check your console URL,
e.g. `falcon.us-2.crowdstrike.com` ⇒ US-2):

| Region | Base URL |
|---|---|
| US-1 (default) | `https://api.crowdstrike.com` |
| US-2 | `https://api.us-2.crowdstrike.com` |
| EU-1 | `https://api.eu-1.crowdstrike.com` |
| US-GOV-1 | `https://api.laggar.gcw.crowdstrike.com` |
| US-GOV-2 | `https://api.us-gov-2.crowdstrike.mil` |

## Required API scopes

Create an API client under **Support and resources > API clients and keys** in
the Falcon console and grant:

| Scope | Permission | Purpose |
|---|---|---|
| Incidents | Read | Incidents and behaviors |
| Alerts | Read | Alerts |
| Hosts | Read | Devices |
| NGSIEM | Read + Write | EDR event search (Write creates the async query job; no data is modified) |

## Tools

| Tool | Description |
|---|---|
| `falcon_search_incidents` | Search incidents by FQL; returns full details, paginated |
| `falcon_get_incidents` | Get incident details by IDs |
| `falcon_search_alerts` | Search alerts by FQL; returns full alert objects, paginated |
| `falcon_get_alerts` | Get alert details by composite IDs |
| `falcon_search_behaviors` | Search behaviors by FQL, paginated |
| `falcon_get_behaviors` | Get behavior details by IDs |
| `falcon_search_devices` | Search devices (hosts) by FQL, paginated |
| `falcon_get_devices` | Get device details by IDs |
| `falcon_get_crowdscores` | Get CrowdScore values (overall threat level) |
| `falcon_search_events` | Search raw EDR events with CQL via Next-Gen SIEM (async, polled), paginated |

The `get_*` tools clamp their input ID list to `maxRecords` and note any dropped
IDs, so the response stays bounded.

## Testing

Mock tests run with no credentials. The live tests run only when
`TEST_FALCON_CLIENT_ID` and `TEST_FALCON_CLIENT_SECRET` are set
(`TEST_FALCON_BASE_URL` optionally selects the cloud region). With just those,
the live suite verifies the token flow, the Hosts/Alerts search tools, and the
full in-memory pagination round-trip (search devices → walk `page_token` pages →
`get_devices`).

Two further live tests are gated by extra environment variables because they
depend on optional API scopes:

| Variable | Enables |
|---|---|
| `TEST_FALCON_INCIDENTS_SCOPE` | incidents / behaviors / CrowdScores. Needs the Incidents:Read scope **and** a tenant whose Incidents API is enabled — some tenants return HTTP 500 even with the scope granted. |
| `TEST_FALCON_EVENTS_QUERY` | Next-Gen SIEM event search (NGSIEM Read+Write scope); value is the CQL query to run |

See the repo-root `.env.example.hcl` for samples.

```sh
go -C falcon test ./...            # mock tests
zenv go -C falcon test ./...       # include the live tests
```
