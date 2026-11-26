terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = "~> 3.5.0"
    }
    # a two-segment pessimistic constraint: ripple moves it to ~> 3.2 for a 3.2.x release
    null = {
      source  = "hashicorp/null"
      version = "~> 3.1"
    }
  }
}

module "label" {
  source  = "cloudposse/label/null"
  version = "0.24.1"

  namespace = "ripple"
  name      = "realtool"
}

resource "random_id" "suffix" {
  byte_length = 4
}

resource "null_resource" "marker" {}

output "id" {
  value = "${module.label.id}-${random_id.suffix.hex}"
}
