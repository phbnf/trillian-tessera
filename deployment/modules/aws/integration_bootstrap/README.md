# Continuous Integration piepline

This directory documents Trillian Tessera's GitHub Actions AWS continuous
integration pipeline, defined in the [aws_integration_test.yml]([/.github/workflows/aws_integration_test.yml.) workflow.

On every merge on the `main` branch, a GitHub Action starts and goes through
the following steps:
 1. Authenticates with AWS
 1. Checks out the code
 1. Logs in with AWS Elastic Container Registry (ECR)
 1. Builds the [conformance](/cmd/conformance/aws/) image
 1. Builds the [hammer](/internal/hammer/) image
 1. Brings down any previous Trillian Tessera AWS conformance infrastructure using `terragrunt`
 1. Brings up a new [Trillian Tessera AWS conformance infrastructure](/deploymet/live/aws/conformance/ci/) using `terragrunt`
 1. Runs the [hammer](/internal/hammer/) against the conformance infrastructure
 1. Brings down the Trillian Tessera AWS conformance infrastructure using `terragrunt`

## Bootstrap

Follow these steps to set up such a pipeline:

 1. In IAM, create a policy matching every `.json` file in this directory. Replace <ACCOUNT_ID> with your AccountID. Note that these policies are not scoped to specific resource instances, and will apply to any resource of the right type. Feel free to scope them further. \
 **! WARNING !** -  These policies are **not** meant for production use, use at your own risk.

 2. [Create an ECS task execution role](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_execution_IAM_role.html#create-task-execution-role). It will be used by ECS an Fargate to grants agents permissions to make API calls. Add the `conformance_createloggroup.json` policy to this role, it will allow ECS to create Cloud Watch log groups on first use.
  
 4. [Create an ECS service linked role](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_create-service-linked-role.html#create-service-linked-role) with `aws iam create-service-linked-role --aws-service-name ecs.amazonaws.com`. This will be [used by ECS to manage ECS clusters](https://docs.aws.amazon.com/aws-managed-policy/latest/reference/AmazonECSServiceRolePolicy.html).
  
 6. Create a `ConformanceECSTaskRolePolicy` role. It will be used to run conformance container. Attach the `conformance_container.json` policy to it, to allow the container to talk to S3.
   
 7. Create a user on AWS called `github-conformance-test`. It will be used by GitHub Actions to authenticate with AWS.
   
 8. Get secret and access keys for this user, and store them in GitHub Actions secrets and variables as `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`.

 9.  Attach the following policies to it:
     - [conformance_auth.json](conformance_auth.json)
     - [conformance_container.json](conformance_container.json)
     - [conformance_createloggroup.json](conformance_createloggroup.json)
     - [conformance_ecr.json](conformance_ecr.json)
     - [conformance_ecsec2.json](conformance_ecsec2.json)
     - [conformance_rds.json](conformance_rds.json)
     - [conformance_s3.json](conformance_s3.json)
     - [conformance_secrets.json](conformance_secrets.json)
     - [conformance_servicediscovery.json](conformance_servicediscovery.json)
     - [conformance_terragrunt.json](conformance_terragrunt.json)
  
 10. Create two Elastic Container Registries, one called `trillian-tessera/conformance`, the other `trillian-tessera/hammer`. We'll use them to store container images.

 11. Submit your workflow file.
