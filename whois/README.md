# whois

WHOIS registration lookups for domain names and IP addresses, for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework.

```
github.com/gollem-dev/tools/whois
```

## Tools

| Name | Description |
|------|-------------|
| `whois_domain` | Perform a WHOIS lookup for a domain name. |
| `whois_ip` | Perform a WHOIS lookup for an IP address (IPv4/IPv6). |

## Usage

```go
ts, err := whois.New()
if err != nil {
	return err
}
if err := ts.Ping(ctx); err != nil { // optional preflight
	return err
}
```

No credentials are required; WHOIS queries go directly to the responsible
registries.

## Options

| Option | Required | Default |
|--------|----------|---------|
| `WithLogger(*slog.Logger)` | no | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when
`TEST_WHOIS_DOMAIN` and `TEST_WHOIS_IP` are set:

```sh
TEST_WHOIS_DOMAIN=example.com TEST_WHOIS_IP=8.8.8.8 go test ./...
```
