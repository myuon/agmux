terraform {
  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 4.0"
    }
  }
  required_version = ">= 1.5"
}

provider "cloudflare" {
  api_token = data.external.op_secrets.result["cloudflare_api_token"]
}

data "external" "op_secrets" {
  program = ["bash", "-c", <<-EOF
    echo '{'
    echo '"cloudflare_api_token":"'$(op read "op://Secrets/agmux Cloudflare Access/cloudflare_api_token")'",'
    echo '"cloudflare_account_id":"'$(op read "op://Secrets/agmux Cloudflare Access/cloudflare_account_id")'",'
    echo '"google_client_id":"'$(op read "op://Secrets/agmux Cloudflare Access/google_client_id")'",'
    echo '"google_client_secret":"'$(op read "op://Secrets/agmux Cloudflare Access/google_client_secret")'"'
    echo '}'
  EOF
  ]
}

locals {
  account_id           = data.external.op_secrets.result["cloudflare_account_id"]
  google_client_id     = data.external.op_secrets.result["google_client_id"]
  google_client_secret = data.external.op_secrets.result["google_client_secret"]
}
