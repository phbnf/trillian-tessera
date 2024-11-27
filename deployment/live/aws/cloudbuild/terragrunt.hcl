terraform {
  source = "${get_repo_root()}/deployment/modules/aws//cloudbuild"
}

locals {
  project_id = "phboneff-dev"
  region     = "us-central1"
  env        = path_relative_to_include()
}

remote_state {
  backend = "gcs"

  config = {
    project  = local.project_id
    location = local.region
    bucket   = "${local.project_id}-tessera-cloudbuild-${local.env}-terraform-state"
    prefix   = "${path_relative_to_include()}-terraform.tfstate"

    gcs_bucket_labels = {
      name = "terraform_state_storage"
    }
  }
}
