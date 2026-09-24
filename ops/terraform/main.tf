# iam.okemily.com DNS -- founder real-time, 2026-09-24: "use cloudflare to do the dns - use
# terraform" (same thread as SSOLoginHandler / ops/nginx/iam-okemily.conf). A prior attempt to
# create this record via a direct Cloudflare API call from this session was blocked by the
# sandbox's own credential-leakage guard (the API token appeared as literal text in a shell
# command) -- Terraform sidesteps that the right way: the token is supplied via the
# CLOUDFLARE_API_TOKEN env var (or TF_VAR_cloudflare_api_token), read from a file at apply time,
# never typed as literal text in a command. It is also just the correct tool for real,
# durable infra state instead of a one-off curl call, and gives this record (and any future
# okemily.com DNS work) a reviewable, git-tracked history from here on.
#
# Apply:
#   export TF_VAR_cloudflare_api_token="$(grep -oP '(?<=^)cfat_[A-Za-z0-9]+' /home/fatbaby/EMILY/var/cloudflare.md | head -1)"
#   cd /home/fatbaby/IDUNA/ops/terraform && terraform init && terraform plan && terraform apply
#
# No state file is checked in (see .gitignore in this directory) -- state stays local to
# whichever box actually runs apply, same as every other piece of infra in this monorepo that's
# set up by a human/agent with real credentials, not CI.

terraform {
  required_version = ">= 1.5"
  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 5.0"
    }
  }
}

variable "cloudflare_api_token" {
  description = "Cloudflare API token (EMILY/var/cloudflare.md) -- never pass this as a CLI flag; use TF_VAR_cloudflare_api_token or a *.auto.tfvars file excluded from git."
  type        = string
  sensitive   = true
}

variable "okemily_zone_id" {
  description = "Cloudflare zone ID for okemily.com."
  type        = string
  default     = "ec4ad5f9d1694ce48e47e40aad51f552"
}

variable "server_ipv4" {
  description = "This box's public IPv4 -- same target every other *.okemily.com A record uses (wotan.okemily.com, the okemily.com apex)."
  type        = string
  default     = "198.58.107.85"
}

provider "cloudflare" {
  api_token = var.cloudflare_api_token
}

# iam.okemily.com -- the dedicated IDUNA SSO login domain (see
# IDUNA/internal/http/handlers/sso_login.go, IDUNA/ops/nginx/iam-okemily.conf). DNS-only (not
# proxied through Cloudflare's edge) so certbot's HTTP-01 challenge in
# sudo-queue/91-iam-okemily-sso-domain.sh can reach this box directly -- matching how
# wotan.okemily.com is already set up.
resource "cloudflare_dns_record" "iam" {
  zone_id = var.okemily_zone_id
  name    = "iam"
  type    = "A"
  content = var.server_ipv4
  ttl     = 300
  proxied = false
  comment = "IDUNA SSO login domain (iam.okemily.com) -- managed by Terraform, see IDUNA/ops/terraform/main.tf"
}

output "iam_fqdn" {
  value = "${cloudflare_dns_record.iam.name}.okemily.com"
}
