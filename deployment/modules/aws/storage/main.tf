terraform {
  backend "s3" {}
  required_providers {
    aws = {
      source = "hashicorp/aws"
      version = "5.76.0"
    }
  }
}

# Configure the AWS Provider
provider "aws" {
  region = var.region
}

# Resources

## S3 Bucket
resource "aws_s3_bucket" "log_bucket" {
  bucket = "${var.bucket_prefix}-${var.base_name}-bucket"
  force_destroy = var.ephemeral
}

resource "aws_rds_cluster" "log_rds" {
  cluster_identifier      = var.base_name
  engine                  = "aurora-mysql"
  availability_zones      = ["us-east-1a", "us-east-1b"]
  database_name           = "tessera"
  master_username         = "root"
  master_password         = "password"
}