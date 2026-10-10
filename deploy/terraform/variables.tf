variable "aws_region" {
  description = "AWS region to deploy into."
  type        = string
  default     = "us-east-1"
}

variable "name" {
  description = "Name prefix for all created resources."
  type        = string
  default     = "gochat"
}

variable "instance_type" {
  description = "EC2 instance type. Default is a cheap ARM (Graviton) instance."
  type        = string
  default     = "t4g.nano"
}

variable "architecture" {
  description = "CPU architecture for the AMI: arm64 (Graviton, e.g. t4g) or x86_64 (e.g. t3)."
  type        = string
  default     = "arm64"

  validation {
    condition     = contains(["arm64", "x86_64"], var.architecture)
    error_message = "architecture must be either \"arm64\" or \"x86_64\"."
  }
}

variable "availability_zone" {
  description = "Availability zone to use. Empty picks the first available in the region."
  type        = string
  default     = ""
}

variable "gochat_port" {
  description = "Port the rendezvous node listens on (TCP + UDP)."
  type        = number
  default     = 4001
}

variable "gochat_version" {
  description = "Release tag to install, e.g. \"v0.1.0\", or \"latest\"."
  type        = string
  default     = "latest"
}

variable "github_repo" {
  description = "GitHub OWNER/REPO to install releases from."
  type        = string
  default     = "Satyam-1o/gochat"
}

variable "install_script_ref" {
  description = "Git ref of this repo to fetch scripts/install-rendezvous.sh from."
  type        = string
  default     = "main"
}

variable "ssh_key_name" {
  description = "Existing EC2 key pair for SSH. Empty disables key-based SSH (use SSM Session Manager instead)."
  type        = string
  default     = ""
}

variable "ssh_cidr" {
  description = "CIDR allowed to reach SSH (port 22). Empty leaves SSH closed; set to e.g. \"203.0.113.4/32\"."
  type        = string
  default     = ""
}
