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

## Aurora MySQL RDS database
resource "aws_rds_cluster" "log_rds" {
  cluster_identifier      = var.base_name
  engine                  = "aurora-mysql"
  # TODO(phboneff): make sure that we want to pin this
  engine_version          = "8.0.mysql_aurora.3.05.2"
  availability_zones      = ["us-east-1a", "us-east-1b"]
  database_name           = "tessera"
  master_username         = "root"
  master_password         = "password"
  skip_final_snapshot     = true
  apply_immediately       = true
  backup_retention_period = 0
}

resource "aws_rds_cluster_instance" "cluster_instances" {
  writer             = true
  count              = 1
  identifier         = "${var.base_name}-writer-${count.index}"
  cluster_identifier = aws_rds_cluster.log_rds.id
  instance_class     = "db.r5.large"
  engine             = aws_rds_cluster.log_rds.engine
  engine_version     = aws_rds_cluster.log_rds.engine_version
}