# Sample environment for running the live-service tests (zenv HCL format).
#
# Copy this file to `.env.hcl`, fill in real values, and run tests with zenv:
#
#   zenv go -C otx test ./...
#
# Each tool's live test (TestLive) runs ONLY when its TEST_* variables are set;
# otherwise it is skipped. Secrets are marked with `secret = true`.

# --- otx (AlienVault OTX) ---
TEST_OTX_API_KEY {
  value  = "your-otx-api-key"
  secret = true
}

# --- vt (VirusTotal) ---
TEST_VT_API_KEY {
  value  = "your-virustotal-api-key"
  secret = true
}

# --- abusech (abuse.ch MalwareBazaar) ---
TEST_ABUSECH_API_KEY {
  value  = "your-abusech-auth-key"
  secret = true
}

# --- ipdb (AbuseIPDB) ---
TEST_IPDB_API_KEY {
  value  = "your-abuseipdb-api-key"
  secret = true
}

# --- shodan ---
TEST_SHODAN_API_KEY {
  value  = "your-shodan-api-key"
  secret = true
}

# --- whois (no credentials; just targets to query) ---
TEST_WHOIS_DOMAIN = "google.com"
TEST_WHOIS_IP     = "8.8.8.8"

# --- urlscan (urlscan.io) ---
TEST_URLSCAN_API_KEY {
  value  = "your-urlscan-api-key"
  secret = true
}

# --- slack (message search; requires a user token with search:read scope) ---
TEST_SLACK_USER_TOKEN {
  value  = "xoxp-your-user-token"
  secret = true
}
TEST_SLACK_QUERY = "incident"

# --- intune (Microsoft Graph / Azure AD app) ---
TEST_INTUNE_TENANT_ID = "00000000-0000-0000-0000-000000000000"
TEST_INTUNE_CLIENT_ID = "00000000-0000-0000-0000-000000000000"
TEST_INTUNE_CLIENT_SECRET {
  value  = "your-client-secret"
  secret = true
}
TEST_INTUNE_USER = "alice@example.com"
# Optional: enables the intune_devices_by_hostname sub-case.
TEST_INTUNE_HOSTNAME = "LAPTOP-001"

# --- github (GitHub App credentials + a target repo) ---
TEST_GITHUB_APP_ID              = "123456"
TEST_GITHUB_APP_INSTALLATION_ID = "12345678"
TEST_GITHUB_APP_PRIVATE_KEY {
  file   = "github-app-private-key.pem"
  secret = true
}
TEST_GITHUB_REPO = "owner/repo"

# --- bigquery (only PROJECT_ID is required to enable the live test) ---
TEST_BIGQUERY_PROJECT_ID    = "your-gcp-project"
TEST_BIGQUERY_CONFIG        = "bigquery-config.yaml"
TEST_BIGQUERY_STORAGE_BUCKET = "your-results-bucket"
TEST_BIGQUERY_DATASET       = "your_dataset"
TEST_BIGQUERY_TABLE         = "your_table"
# Optional: enables the bigquery_query -> bigquery_result sub-case
# (also needs TEST_BIGQUERY_STORAGE_BUCKET).
TEST_BIGQUERY_QUERY = "SELECT 1 AS n"
TEST_BIGQUERY_CREDENTIALS {
  file   = "bigquery-credentials.json"
  secret = true
}

# --- webfetch (basic live test needs only a URL; LLM test adds a Gemini project) ---
TEST_WEBFETCH_URL      = "https://www.google.com"
TEST_GEMINI_PROJECT_ID = "your-gcp-project"
TEST_GEMINI_LOCATION   = "global"
