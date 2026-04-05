#!/bin/bash
set -euo pipefail

VAULT="Secrets"
ITEM_TITLE="agmux Cloudflare Access"

echo "=== agmux Cloudflare Access secrets setup ==="
echo ""

# Create vault if not exists
if ! op vault get "$VAULT" > /dev/null 2>&1; then
  echo "Creating vault: $VAULT"
  op vault create "$VAULT" > /dev/null
fi

read -rp "Cloudflare Account ID: " CF_ACCOUNT_ID
read -rp "Cloudflare API Token: " CF_API_TOKEN
read -rp "Google OAuth Client ID: " GOOGLE_CLIENT_ID
read -rsp "Google OAuth Client Secret: " GOOGLE_CLIENT_SECRET
echo ""

# Check if item already exists
if op item get "$ITEM_TITLE" --vault="$VAULT" > /dev/null 2>&1; then
  echo "Updating existing item..."
  op item edit "$ITEM_TITLE" \
    --vault="$VAULT" \
    "cloudflare_account_id=$CF_ACCOUNT_ID" \
    "cloudflare_api_token=$CF_API_TOKEN" \
    "google_client_id=$GOOGLE_CLIENT_ID" \
    "google_client_secret=$GOOGLE_CLIENT_SECRET" \
    > /dev/null
  echo "Updated: $ITEM_TITLE"
else
  echo "Creating new item..."
  op item create \
    --category=login \
    --title="$ITEM_TITLE" \
    --vault="$VAULT" \
    "cloudflare_account_id=$CF_ACCOUNT_ID" \
    "cloudflare_api_token=$CF_API_TOKEN" \
    "google_client_id=$GOOGLE_CLIENT_ID" \
    "google_client_secret=$GOOGLE_CLIENT_SECRET" \
    > /dev/null
  echo "Created: $ITEM_TITLE"
fi

echo ""
echo "Done. Terraform can read these via:"
echo '  op read "op://Secrets/agmux Cloudflare Access/cloudflare_api_token"'
