variable "domain" {
  type        = string
  description = "Domain to protect with Cloudflare Access"
}

variable "app_name" {
  type        = string
  description = "Application name in Cloudflare Access"
}

variable "allowed_emails" {
  type        = list(string)
  description = "Email addresses allowed to access the application"
}

variable "session_duration" {
  type        = string
  default     = "730h"
  description = "Session duration (Go duration format, e.g. 730h = ~30 days)"
}
