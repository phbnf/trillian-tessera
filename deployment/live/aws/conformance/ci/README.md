# AWS Conformance Configs

Work in progress

## Prequisites

You'll need to have configured the right IAM permissions to create S3 buckets
and RDS databases, and configured a local AWS profile that can make use of
these permissions.

## Manual deployment 

Set the required environment variables:
```bash
export AWS_PROFILE={VALUE}
```

Optionally, customize the AWS region (defaults to "us-east-1"),
and common resource names (defaults to "conformance"):
```bash
export AWS_REGION={VALUE}
export TESSERA_BASE_NAME={VALUE}
```

Terraforming the project can be done by:
 1. `cd` to the relevant directory for the environment to deploy/change (e.g. `ci`)
 2. Run `terragrunt apply`

