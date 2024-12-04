output "log_bucket_id" {
  description = "Log S3 bucket name"
  value       = module.storage.log_bucket.id
}

output "log_rds_db" {
  description = "Log RDS database address"
  value       = module.storage.log_rds_db.address
}

output "log_name" {
  description = "Log name"
  value       = local.name
}
