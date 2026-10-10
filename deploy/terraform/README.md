# gochat on AWS (Terraform)

Provisions a small, always-on EC2 instance running the gochat rendezvous/relay
node, with a static public IP so clients' saved bootstrap addresses keep working.

## What it creates

- A dedicated VPC with a public subnet, internet gateway, and route table.
- A security group opening `gochat_port` (default `4001`) for **TCP and UDP**
  (QUIC), plus optional SSH.
- An **Amazon Linux 2023** EC2 instance (default `t4g.micro`, ARM) with
  **IMDSv2 required**.
- An **Elastic IP** (static public address).
- An IAM instance profile for **SSM Session Manager**, so you can get a shell
  without managing SSH keys.
- Cloud-init user-data that runs
  [`scripts/install-rendezvous.sh`](../../scripts/install-rendezvous.sh): it
  downloads the release binary, installs a hardened systemd service, and starts it.

## Prerequisites

- [Terraform](https://developer.hashicorp.com/terraform/install) >= 1.5
- AWS credentials configured (`aws configure`, or `AWS_PROFILE`, or env vars)
- Permissions to create VPC/EC2/IAM resources in the target account

## Quick start

```sh
cd deploy/terraform
cp terraform.tfvars.example terraform.tfvars   # optional; edit as needed
terraform init
terraform plan
terraform apply
```

`apply` prints `public_ip`, `instance_id`, and an `access` hint.

## Get the client bootstrap address

The node takes ~30–60 seconds to install after boot. Then read its log:

```sh
# Keyless shell via SSM Session Manager:
aws ssm start-session --target "$(terraform output -raw instance_id)"

# On the node:
sudo journalctl -u gochat-rendezvous -n 40 --no-pager
```

Copy the line starting with `client: gochat -bootstrap "..."` and give it to
clients:

```sh
gochat -bootstrap "/ip4/<public-ip>/tcp/4001/p2p/<peer-id>"
```

You can also just build the address yourself from `terraform output -raw public_ip`
plus the peer ID — but the node prints it ready to paste, so prefer the log.

## SSH access (optional)

By default SSH is closed and you use SSM. To enable key-based SSH, set both:

```hcl
ssh_key_name = "my-key"             # an existing EC2 key pair in the region
ssh_cidr     = "203.0.113.4/32"     # your IP — never 0.0.0.0/0
```

## Variables

| Name | Default | Description |
| --- | --- | --- |
| `aws_region` | `us-east-1` | Region to deploy into |
| `name` | `gochat` | Resource name prefix |
| `instance_type` | `t4g.micro` | EC2 instance type |
| `architecture` | `arm64` | AMI arch; use `x86_64` with e.g. `t3.micro` |
| `availability_zone` | `""` | Empty = first available in the region |
| `gochat_port` | `4001` | Listen port (TCP + UDP) |
| `gochat_version` | `latest` | Release tag to install, e.g. `v0.1.0` |
| `github_repo` | `Satyam-1o/gochat` | Releases repo |
| `install_script_ref` | `main` | Git ref to fetch the install script from |
| `ssh_key_name` | `""` | Optional EC2 key pair |
| `ssh_cidr` | `""` | Optional CIDR allowed to SSH |

Switching to an x86 instance:

```hcl
instance_type = "t3.micro"
architecture  = "x86_64"
```

## Cost

Roughly **$9–11/month** in `us-east-1` for a `t4g.micro` (on-demand) plus the
public IPv4 address charge. See the
[AWS Free Tier](https://aws.amazon.com/free/) for whether your account has
credits. Destroy the stack when you're done to stop the charges.

## Teardown

```sh
terraform destroy
```

The node's identity key lives on the instance's root volume and is deleted with
it; a fresh deploy gets a **new peer ID**, so clients must be updated with the
new bootstrap address.

## Notes

- **Static IP matters.** Clients save the bootstrap address; the Elastic IP keeps
  it stable across reboots.
- **UDP can be blocked.** If QUIC/UDP is unavailable, clients fall back to TCP —
  just make sure `gochat_port/tcp` is reachable.
- **The node sees no plaintext** — only hashed room topics, peer IDs, and
  ciphertext.
- **Ongoing updates:** re-running the installer on the box upgrades the binary:

  ```sh
  sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/Satyam-1o/gochat/main/scripts/install-rendezvous.sh)"
  ```
