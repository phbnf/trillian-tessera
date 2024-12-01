variable "prefix_name" {
  description = "Common prefix to use when naming resources, ensures unicity of the s3 bucket name."
  type        = string
}

variable "base_name" {
  description = "Common name to use when naming resources."
  type        = string
}

variable "region" {
  description = "Region in which to create resources."
  type        = string
}

variable "ephemeral" {
  description = "Set to true if this is a throwaway/temporary log instance. Will set attributes on created resources to allow them to be disabled/deleted more easily."
  type = bool
}

variable "ecr_registry" {
  description = "Container registry address, with the conformance and hammer repositories."
  type        = string
}

variable "ecr_repository_conformance" {
  description = "Container repository for the conformance binary, with the tag."
  type        = string
}

variable "ecr_repository_hammer" {
  description = "Container repository for the hammer binary, with the tag."
  type        = string
}

variable "signer" {
  description = "The note signer which used to sign checkpoints."
  type        = string
}

variable "verifier" {
  description = "The note verifier used to verify checkpoints."
  type        = string
}

variable "ecs_role" {
  description = "Role used to run the ECS containers and task."
  type        = string
}