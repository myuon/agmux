# --- Identity Provider: Google OAuth ---

resource "cloudflare_zero_trust_access_identity_provider" "google" {
  account_id = local.account_id
  name       = "Google"
  type       = "google"

  config {
    client_id     = local.google_client_id
    client_secret = local.google_client_secret
  }
}

# --- Access Application: main (authenticated) ---

resource "cloudflare_zero_trust_access_application" "main" {
  account_id       = local.account_id
  name             = var.app_name
  domain           = var.domain
  type             = "self_hosted"
  session_duration = var.session_duration
  allowed_idps     = [cloudflare_zero_trust_access_identity_provider.google.id]
}

# --- Access Policy: Allow specific emails ---

resource "cloudflare_zero_trust_access_policy" "allow_email" {
  application_id = cloudflare_zero_trust_access_application.main.id
  account_id     = local.account_id
  name           = "Allow specific emails"
  precedence     = 1
  decision       = "allow"

  include {
    email = var.allowed_emails
  }
}

# --- Access Application: manifest.json bypass (for PWA) ---

resource "cloudflare_zero_trust_access_application" "manifest_bypass" {
  account_id       = local.account_id
  name             = "${var.app_name} manifest.json (bypass)"
  domain           = "${var.domain}/manifest.json"
  type             = "self_hosted"
  session_duration = "24h"
}

resource "cloudflare_zero_trust_access_policy" "bypass_manifest" {
  application_id = cloudflare_zero_trust_access_application.manifest_bypass.id
  account_id     = local.account_id
  name           = "Bypass manifest.json"
  precedence     = 1
  decision       = "bypass"

  include {
    everyone = true
  }
}
