# intune

Query Microsoft Intune managed devices via the
[Microsoft Graph](https://learn.microsoft.com/graph/) API, for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework.

```
github.com/gollem-dev/tools/intune
```

## Tools

| Name | Description |
|------|-------------|
| `intune_devices_by_user` | Search managed devices by user email / UPN. |
| `intune_devices_by_hostname` | Search a managed device by hostname. |

## Usage

```go
ts, err := intune.New(
	intune.WithTenantID("..."),
	intune.WithClientID("..."),
	intune.WithClientSecret("..."),
)
if err != nil {
	return err
}
if err := ts.Ping(ctx); err != nil { // optional preflight
	return err
}
```

Authentication uses the OAuth 2.0 client-credentials flow against the tenant's
token endpoint (derived from `TenantID`, overridable with `WithTokenEndpoint`).

## Options

| Option | Required | Default |
|--------|----------|---------|
| `WithTenantID(string)` | yes | — |
| `WithClientID(string)` | yes | — |
| `WithClientSecret(string)` | yes | — |
| `WithBaseURL(string)` | no | `https://graph.microsoft.com/v1.0` |
| `WithTokenEndpoint(string)` | no | derived from `TenantID` |
| `WithHTTPClient(*http.Client)` | no | `http.DefaultClient` |
| `WithLogger(*slog.Logger)` | no | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when the
`TEST_INTUNE_*` variables are set:

```sh
TEST_INTUNE_TENANT_ID=... TEST_INTUNE_CLIENT_ID=... TEST_INTUNE_CLIENT_SECRET=... \
	TEST_INTUNE_USER=user@example.com TEST_INTUNE_HOSTNAME=host01 go test ./...
```
