terraform {
  required_providers {
    asc = {
      source = "NL-AMS-DSPC/asc"
    }
  }
}

# Every argument can also come from an environment variable, which keeps
# credentials out of the configuration:
#
#   ASC_ENDPOINT   -> endpoint   ASC API base URL
#   ASC_AUTH_URL   -> auth_url   identity provider base URL
#   ASC_ORG        -> org        realm the OAuth client belongs to
#   ASC_USERNAME   -> username   OAuth client ID
#   ASC_PASSWORD   -> password   OAuth client secret
#   ASC_NAMESPACE  -> namespace  tenant to manage
#   ASC_TIMEOUT    -> timeout    HTTP timeout in seconds (default 30)
#
# Arguments set in the provider block take precedence over the environment.
provider "asc" {
  endpoint  = "https://api.example.com"
  auth_url  = "https://auth.example.com"
  org       = "example-org"
  username  = "terraform-client"
  namespace = "example-tenant"
  timeout   = 60

  # password is deliberately omitted: export ASC_PASSWORD instead of
  # storing the client secret in the configuration.
}
