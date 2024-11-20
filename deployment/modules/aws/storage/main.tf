terraform {
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
resource "aws_s3_bucket" "example" {
  bucket = "${var.bucket_prefix}-${var.base_name}-bucket"
  force_destroy = var.ephemeral
}

