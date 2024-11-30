output "vpc_subnets" {
    description = "VPC subnets list"
    value       = data.aws_subnets.subnets
}