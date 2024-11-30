terraform {
  backend "s3" {}
  required_providers {
    aws = {
      source = "hashicorp/aws"
      version = "5.76.0"
    }
  }
}

data "aws_caller_identity" "current" {}

locals {
  name = "${var.prefix_name}-${var.base_name}"
}

# Configure the AWS Provider
provider "aws" {
  region = var.region
}

module "storage" {
  source = "../storage"

  prefix_name = var.prefix_name
  base_name   = var.base_name
  region      = var.region
  ephemeral   = true
}

# Resources

## ECS cluster
resource "aws_ecs_cluster" "conformance-test" {
  name = "${local.name}"
}

resource "aws_ecs_cluster_capacity_providers" "conformance-test" {
  cluster_name = aws_ecs_cluster.conformance-test.name

  capacity_providers = ["FARGATE"]
}

resource "aws_ecs_task_definition" "conformance" {
  family                   = "conformance"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = 1024
  memory                   = 2048
  # TODO(phboneff): change this
  task_role_arn            = "arn:aws:iam::869935063533:role/ecsTaskExecutionRole"
  execution_role_arn       = "arn:aws:iam::869935063533:role/ecsTaskExecutionRole"
  container_definitions = jsonencode([
    {
            "name": "${local.name}-conformance",
            "image": "${var.ecr_registry}/${var.ecr_repository_conformance}:latest",
            "cpu": 0,
            "portMappings": [
                {
                    "name": "conformance-2024-tcp",
                    "containerPort": 2024,
                    "hostPort": 2024,
                    "protocol": "tcp",
                    "appProtocol": "http"
                }
            ],
            "essential": true,
            "command": [
                "--signer",
                "PRIVATE+KEY+phboneff-dev-ci-conformance+3f5267c1+AbNthDVVl8SUoHuxMtSxGjHXi5R+CivYtyO7M2TPVSi6",
                "--bucket",
                "phboneff-dev-ci-conformance-bucket",
                "--db_user",
                "root",
                "--db_password",
                "password",
                "--db_name",
                "tessera",
                "--db_host",
                "phboneff-dev-ci-conformance-writer-0.cbw8guwmo1hn.us-east-1.rds.amazonaws.com",
                "-v",
                "2"
            ],
            "logConfiguration": {
                "logDriver": "awslogs",
                "options": {
                    "awslogs-group": "/ecs/${local.name}",
                    "mode": "non-blocking",
                    "awslogs-create-group": "true",
                    "max-buffer-size": "25m",
                    "awslogs-region": "us-east-1",
                    "awslogs-stream-prefix": "ecs"
                },
            },
        },
  ])

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "X86_64"
  }
}

resource "aws_ecs_task_definition" "hammer" {
  family                   = "hammer"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = 1024
  memory                   = 2048
  # TODO(phboneff): change this
  task_role_arn            = "arn:aws:iam::869935063533:role/ecsTaskExecutionRole"
  execution_role_arn       = "arn:aws:iam::869935063533:role/ecsTaskExecutionRole"
  container_definitions = jsonencode([
    {
            "name": "${local.name}-hammer",
            "image": "${var.ecr_registry}/${var.ecr_repository_conformance}:latest",
            "cpu": 0,
            "portMappings": [
                {
                    "name": "hammer-80-tcp",
                    "containerPort": 80,
                    "hostPort": 80,
                    "protocol": "tcp",
                    "appProtocol": "http"
                }
            ],
            "essential": true,
            "command": [
                "--log_public_key=phboneff-dev-ci-conformance+3f5267c1+AatjnH2pMn2wRamVV1hywQI/+lHsV8ftCBroiCWyOUWQ",
                "--log_url=https://phboneff-dev-ci-conformance-bucket.s3.us-east-1.amazonaws.com",
                "--write_log_url=http://${aws_service_discovery_service.conformance-discovery.name}.${aws_service_discovery_private_dns_namespace.internal.name}:2024",
                "-v=3",
                "--show_ui=false",
                "--logtostderr",
                "--num_writers=1100",
                "--max_write_ops=1500",
                "--leaf_min_size=1024",
                "--leaf_write_goal=50000"
            ],
            "logConfiguration": {
                "logDriver": "awslogs",
                "options": {
                    "awslogs-group": "/ecs/${local.name}-hammer",
                    "mode": "non-blocking",
                    "awslogs-create-group": "true",
                    "max-buffer-size": "25m",
                    "awslogs-region": "us-east-1",
                    "awslogs-stream-prefix": "ecs"
                },
            },
        }
  ])

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "X86_64"
  }
}

resource "aws_service_discovery_private_dns_namespace" "internal" {
  name        = "internal"
  vpc         = aws_default_vpc.default.id
}

resource "aws_service_discovery_service" "conformance-discovery" {
  name = "conformance-discovery"

  dns_config {
    namespace_id = aws_service_discovery_private_dns_namespace.internal.id

    dns_records {
      ttl  = 10
      type = "A"
    }

    // TODO(phboneff): make sure that the hammer uses multiple IPs
    // otherwise, set a low TTL and use WEIGHTED.
    routing_policy = "MULTIVALUE"
  }

  health_check_custom_config {
    failure_threshold = 1
  }
}

resource "aws_ecs_service" "conformance_service" {
  name            = "${local.name}"
  cluster         = aws_ecs_cluster.conformance-test.arn
  task_definition = aws_ecs_task_definition.conformance.arn
  desired_count   = 3
  network_configuration {
    subnets = data.aws_subnets.subnets.ids
    # required to access container registry
    assign_public_ip = true
  }
  force_new_deployment = true
  # redeployment on every apply
  triggers = {
    redeployment = plantimestamp()
  }

  launch_type = "FARGATE"

  service_registries {
    registry_arn = aws_service_discovery_service.conformance-discovery.arn
  }
  
  depends_on = [
    aws_service_discovery_private_dns_namespace.internal,
    aws_service_discovery_service.conformance-discovery
  ]
}

resource "aws_default_vpc" "default" {
  tags = {
    Name = "Default VPC"
  }
}

resource "aws_vpc_endpoint" "s3" {
  vpc_id       = aws_default_vpc.default.id
  service_name = "com.amazonaws.${var.region}.s3"
}

data "aws_subnets" "subnets" {
  filter {
    name   = "vpc-id"
    values = [aws_default_vpc.default.id]
  }
}

resource "aws_vpc_endpoint_route_table_association" "private_s3" {
  vpc_endpoint_id = aws_vpc_endpoint.s3.id
  route_table_id  = aws_default_vpc.default.default_route_table_id
}

resource "aws_s3_bucket_policy" "allow_access_from_vpce" {
  bucket = module.storage.log_bucket.id
  policy = data.aws_iam_policy_document.allow_access_from_vpce.json
}

data "aws_iam_policy_document" "allow_access_from_vpce" {
  statement {
    principals {
      type        = "*"
      identifiers = ["*"]
    }

    actions = [
      "s3:GetObject",
    ]

    resources = [
      "${module.storage.log_bucket.arn}/*",
    ]

    condition {
     test = "StringEquals"
     variable = "aws:sourceVpce" 
     # TODO(phboneff): replace with real VPC ID
     values = [aws_vpc_endpoint.s3.id]
    }
  }
  depends_on = [aws_vpc_endpoint.s3]
}