terraform {
  source = "${get_repo_root()}/deployment/modules/aws//storage"
}

locals {
  env        = path_relative_to_include()
  account_id = "${get_aws_account_id()}"
  region     = get_env("AWS_REGION", "us-east-1")
  base_name  = get_env("TESSERA_BASE_NAME", "${local.env}-conformance")
}

remote_state {
  backend = "s3"

  config = {
    region         = local.region
    bucket         = "${local.account_id}-${local.base_name}-terraform-state"
    key            = "${local.env}/terraform.tfstate"
    dynamodb_table = "${local.account_id}-${local.base_name}-terraform-lock"
    s3_bucket_tags = {
      name = "terraform_state_storage"
    }
  }
}