locals {
  name = "${var.prefix_name}-${var.base_name}"
}

# Configure the AWS Provider
provider "aws" {
  region = var.region
}

# Resources

## S3 Bucket
resource "aws_s3_bucket" "log_bucket" {
  bucket = "${local.name}-bucket"
  force_destroy = var.ephemeral
}

## MySQL RDS database
resource "aws_db_instance" "log_rds" {
  allocated_storage    = 10
  db_name              = "tessera"
  engine               = "mysql"
  engine_version       = "8.0.39"
  instance_class       = "db.t3.micro"
  username             = "root"
  password             = "password"
  parameter_group_name = "default.mysql8.0"
  skip_final_snapshot  = true
}
