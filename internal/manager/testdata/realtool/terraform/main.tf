terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = "~> 3.5.0"
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

output "id" {
  value = "${module.label.id}-${random_id.suffix.hex}"
}
