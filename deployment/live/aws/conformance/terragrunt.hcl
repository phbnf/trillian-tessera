terraform {
  source = "${get_repo_root()}/deployment/modules/aws//storage"
}

locals {
  env        = path_relative_to_include()
  account_id = "864981736166"
  region     = "us-east-1"
  base_name  = get_env("TESSERA_BASE_NAME", "${local.env}-conformance")
}

remote_state {
  backend = "s3"

  config = {
    // TODO(phboneff): Does this need to be set here?
    //project      = local.account_id 
    region         = local.region
    bucket         = "${local.account_id}-${local.base_name}-terraform-state"
    prefix         = "${local.env}/terraform.tfstate"
    encrypt        = false
    
    // TODO(phboneff): Will this have a prefix?
    dynamodb_table = "${local.account_id}-${local.base_name}-terraform-lock"
 
    s3_bucket_tags = {
      // TODO(phboneff): Does this need capitalisation
      name = "terraform_state_storage"
    }
  }
}
