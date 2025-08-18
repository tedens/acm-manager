# acm-manager

acm-manager is a Kubernetes controller that automatically manages public AWS ACM certificates for Ingress resources. It ensures that TLS certificates are created, validated, and attached to your ALBs using annotations and AWS Route 53 DNS records. The controller is designed to integrate seamlessly with ALB Ingress Controller-managed services in AWS EKS.

---

## Features

- Automatically request public ACM certificates based on Ingress annotations
- Validate certificates using DNS through Route 53
- Patch Ingress resources with valid ACM certificate ARNs
- Reuse or replace certificates based on configuration
- Optional cleanup of certificates upon Ingress deletion
- Support for wildcard certificates and zone ID overrides
- Helm chart deployment supported

---

## Prerequisites

- Go v1.25.0+
- Docker v28.3.2+
- kubectl v1.28.2+
- Access to a Kubernetes v1.32+ cluster
- AWS IAM permissions to manage ACM, Route 53, and ALB

---

## Installation

### Build and Push the Image

```bash
make docker-build docker-push IMG=<your-registry>/acm-manager:tag
```


### Deploy the Manager

```bash
make deploy IMG=<your-registry>/acm-manager:tag
```

> **NOTE**: You may need `cluster-admin` privileges.

---

## Using the Controller

The controller watches `Ingress` objects with ACM annotations and automatically manages ACM certificate creation and ALB patching according to those annotations.

---

## Ingress Annotations Reference

The following annotations can be added to your `Ingress` resources to control how `acm-manager` behaves:

| Annotation                                      | Description                                                                 | Type    | Default   | Required |
|------------------------------------------------|-----------------------------------------------------------------------------|---------|-----------|----------|
| `acm.tedens.dev/managed`                       | Enable ACM management for this ingress                                     | `bool`  | `false`   | ✅       |
| `acm.tedens.dev/domain`                        | Override the domain used for the certificate                               | `string`| *(none)*  | ❌       |
| `acm.tedens.dev/zone-id`                       | Override the Route 53 hosted zone ID                                       | `string`| *(auto-discovered)* | ❌ |
| `acm.tedens.dev/wildcard`                      | Request a wildcard certificate                                             | `bool`  | `false`   | ❌       |
| `acm.tedens.dev/reuse-existing`               | Attempt to reuse an existing matching ACM certificate                      | `bool`  | `true`    | ❌       |
| `acm.tedens.dev/delete-cert-on-ingress-delete` | Delete the certificate when the Ingress is deleted                         | `bool`  | `false`   | ❌       |

✅ = Required to trigger ACM management  
❌ = Optional annotations

You must set `acm.tedens.dev/managed: "true"` for the controller to act on the ingress.

---

## Uninstall

```bash
kubectl delete -k config/samples/
make undeploy
make uninstall
```

---

## Helm Chart

You can install this via Helm once the chart is published.

### Local Helm Install

```bash
helm upgrade -i acm-manager ./charts/acm-manager -n acm-manager --create-namespace
```

### Add Repository

```bash
helm repo add acm-manager https://tedens.github.io/acm-manager
helm repo update
helm install acm-manager/acm-manager -n acm-manager --create-namespace
```

---

## GitHub Pages and Chart Distribution

This repo automatically publishes the Helm chart to GitHub Pages using GitHub Actions when changes are made to the `charts/` directory.

A second GitHub Actions workflow regenerates the documentation site hosted on GitHub Pages when the `docs/` folder changes.

---

## Developer Notes

### Generate YAML Bundle

```bash
make build-installer IMG=<your-registry>/acm-manager:tag
```

This creates `dist/install.yaml`.

### Update Helm Chart from Source

```bash
kubebuilder edit --plugins=helm/v1-alpha --force
```

Manually reapply custom Helm settings if overwritten.

---

## IAM Policy

The service account must have IAM permissions for the following:

- `acm:RequestCertificate`
- `acm:DescribeCertificate`
- `acm:DeleteCertificate`
- `route53:ChangeResourceRecordSets`
- `route53:ListHostedZones`
- `route53:ListResourceRecordSets`

---

## Contributing

PRs welcome! Please fork, branch, and submit a pull request with detailed context and reasoning. Run:

```bash
make help
```

To see available development commands.

---

## License

Licensed under the GNU General Public License v3.0. See [LICENSE](./LICENSE) for details.
