output "log_bucket" {
  description = "Log S3 bucket"
  value       = aws_s3_bucket.log_bucket
}

output "log_rds_db" {
  description = "Log RDS database"
  value       = aws_db_instance.log_rds
}
