output "vpc_subnets" {
    description = "VPC subnets list"
    value       = data.aws_subnets.subnets.ids
}

output "ecs_cluster" {
    description = "ECS cluster name"
    value       = aws_ecs_cluster.ecs_cluster.id
}