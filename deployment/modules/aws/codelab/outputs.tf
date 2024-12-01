output "log_bucket" {
  description = "Log S3 bucket endpoint"
  value       = module.storage.log_bucket.bucket_regional_domain_name
}

output "log_rds_db" {
  description = "Log RDS database endpoint"
  value       = module.storage.log_rds_db.endpoint
}
