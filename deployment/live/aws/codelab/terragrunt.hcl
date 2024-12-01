terraform {
  source = "${get_repo_root()}/deployment/modules/aws//codelab"
}

locals {
  region      = get_env("AWS_REGION", "us-east-1")
  base_name   = "codelab"
  prefix_name = "${get_aws_account_id()}-trillian-tessera"
  ephemeral   = true
}

remote_state {
  backend = "s3"

  config = {
    region         = local.region
    bucket         = "${local.prefix_name}-${local.base_name}-terraform-state"
    key            = "terraform.tfstate"
    dynamodb_table = "${local.prefix_name}-${local.base_name}-terraform-lock"
    s3_bucket_tags = {
      name = "terraform_state_storage"
    }
  }
}
