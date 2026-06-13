package whois_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/gollem-dev/tools/whois"
	"github.com/m-mizutani/gt"
)

// fakeWhoisClient is a test double for whoisClient.
type fakeWhoisClient struct {
	result string
	err    error
	gotCtx context.Context
}

func (f *fakeWhoisClient) Whois(ctx context.Context, query string, servers ...string) (string, error) {
	f.gotCtx = ctx
	return f.result, f.err
}

func TestNew(t *testing.T) {
	// New requires no API key; it should always succeed.
	ts, err := whois.New()
	gt.NoError(t, err)
	gt.Value(t, ts).NotNil()
}

func TestSpecs(t *testing.T) {
	ts := gt.R1(whois.New()).NoError(t)

	specs := gt.R1(ts.Specs(context.Background())).NoError(t)
	gt.Array(t, specs).Length(2)

	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
		gt.Map(t, s.Parameters).HasKey("target")
	}
	gt.Array(t, names).Has("whois_domain").Has("whois_ip")
}

// TestRunPropagatesContextDeadline guards against the regression where Run/Ping
// ignored the caller's context, so a timeout/cancel could not bound the WHOIS
// query. The default client translates the deadline into the likexian client's
// timeout, so the deadline must reach the client unchanged.
func TestRunPropagatesContextDeadline(t *testing.T) {
	fake := &fakeWhoisClient{result: "ok"}
	ts := gt.R1(whois.New(whois.WithWhoisClientForTest(fake))).NoError(t)

	deadline := time.Now().Add(5 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	_ = gt.R1(ts.Run(ctx, "whois_domain", map[string]any{"target": "example.com"})).NoError(t)

	gt.Value(t, fake.gotCtx).NotNil().Required()
	got, ok := fake.gotCtx.Deadline()
	gt.Bool(t, ok).True()
	gt.Value(t, got).Equal(deadline)
}

func TestRunDomain(t *testing.T) {
	fake := &fakeWhoisClient{result: "Domain Name: example.com\nRegistrar: IANA"}
	ts := gt.R1(whois.New(whois.WithWhoisClientForTest(fake))).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "whois_domain", map[string]any{"target": "example.com"})).NoError(t)
	gt.Map(t, result).HasKey("result")
	gt.String(t, result["result"].(string)).Contains("example.com")
}

func TestRunIP(t *testing.T) {
	fake := &fakeWhoisClient{result: "NetRange: 8.8.8.0 - 8.8.8.255\nOrg: Google LLC"}
	ts := gt.R1(whois.New(whois.WithWhoisClientForTest(fake))).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "whois_ip", map[string]any{"target": "8.8.8.8"})).NoError(t)
	gt.Map(t, result).HasKey("result")
	gt.String(t, result["result"].(string)).Contains("Google")
}

func TestRunInvalidName(t *testing.T) {
	fake := &fakeWhoisClient{result: "ignored"}
	ts := gt.R1(whois.New(whois.WithWhoisClientForTest(fake))).NoError(t)

	_, err := ts.Run(context.Background(), "whois_unknown", map[string]any{"target": "example.com"})
	gt.Error(t, err).Contains("invalid function name")
}

func TestRunMissingTarget(t *testing.T) {
	fake := &fakeWhoisClient{result: "ignored"}
	ts := gt.R1(whois.New(whois.WithWhoisClientForTest(fake))).NoError(t)

	_, err := ts.Run(context.Background(), "whois_domain", map[string]any{})
	gt.Error(t, err).Contains("target is required")
}

func TestRunClientError(t *testing.T) {
	fake := &fakeWhoisClient{err: errors.New("network failure")}
	ts := gt.R1(whois.New(whois.WithWhoisClientForTest(fake))).NoError(t)

	_, err := ts.Run(context.Background(), "whois_ip", map[string]any{"target": "1.2.3.4"})
	gt.Error(t, err).Contains("failed to query whois")
}

func TestPing(t *testing.T) {
	fake := &fakeWhoisClient{result: "OrgName: Google LLC"}
	ts := gt.R1(whois.New(whois.WithWhoisClientForTest(fake))).NoError(t)

	gt.NoError(t, ts.Ping(context.Background()))
}

func TestPingError(t *testing.T) {
	fake := &fakeWhoisClient{err: errors.New("connection refused")}
	ts := gt.R1(whois.New(whois.WithWhoisClientForTest(fake))).NoError(t)

	err := ts.Ping(context.Background())
	gt.Error(t, err).Contains("whois ping failed")
}

// TestLive hits the real WHOIS service. It runs only when both
// TEST_WHOIS_DOMAIN and TEST_WHOIS_IP are set.
func TestLive(t *testing.T) {
	domain, ok := os.LookupEnv("TEST_WHOIS_DOMAIN")
	if !ok {
		t.Skip("TEST_WHOIS_DOMAIN is not set")
	}
	ip, ok := os.LookupEnv("TEST_WHOIS_IP")
	if !ok {
		t.Skip("TEST_WHOIS_IP is not set")
	}

	ts := gt.R1(whois.New()).NoError(t)

	gt.NoError(t, ts.Ping(context.Background())).Required()

	domainResult := gt.R1(ts.Run(context.Background(), "whois_domain", map[string]any{"target": domain})).NoError(t)
	gt.Map(t, domainResult).HasKey("result")
	gt.String(t, domainResult["result"].(string)).NotEqual("")

	ipResult := gt.R1(ts.Run(context.Background(), "whois_ip", map[string]any{"target": ip})).NoError(t)
	gt.Map(t, ipResult).HasKey("result")
	gt.String(t, ipResult["result"].(string)).NotEqual("")
}
