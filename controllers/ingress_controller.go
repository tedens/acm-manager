package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const ingressFinalizer = "acm.tedens.dev/finalizer"

type IngressReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ACMClient     *acm.Client
	Route53Client *route53.Client
}

// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var ingress networkingv1.Ingress
	if err := r.Get(ctx, req.NamespacedName, &ingress); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cfg := ParseIngressAnnotations(ingress.GetAnnotations())
	if !cfg.Managed {
		return ctrl.Result{}, nil
	}

	domain := cfg.DomainOverride
	if domain == "" && len(ingress.Spec.Rules) > 0 {
		domain = ingress.Spec.Rules[0].Host
	}

	if ingress.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&ingress, ingressFinalizer) {
			controllerutil.AddFinalizer(&ingress, ingressFinalizer)
			if err := r.Update(ctx, &ingress); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(&ingress, ingressFinalizer) {
			if cfg.DeleteCertOnIngress {
				logger.Info("Ingress is being deleted. Deleting associated ACM certificate...", "domain", domain)
				if err := r.deleteCertificateForDomain(ctx, domain); err != nil {
					logger.Error(err, "Failed to delete ACM certificate")
					return ctrl.Result{}, err
				}
			}
			controllerutil.RemoveFinalizer(&ingress, ingressFinalizer)
			if err := r.Update(ctx, &ingress); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil
	}

	logger.Info("Reconciling managed Ingress", "name", req.NamespacedName, "domain", domain)

	certArn, err := r.ensureCertificate(ctx, domain, cfg)
	if err != nil {
		logger.Error(err, "failed to ensure certificate")
		return ctrl.Result{}, err
	}

	patch := client.MergeFrom(ingress.DeepCopy())
	if ingress.Annotations == nil {
		ingress.Annotations = map[string]string{}
	}
	ingress.Annotations["alb.ingress.kubernetes.io/certificate-arn"] = certArn

	if err := r.Patch(ctx, &ingress, patch); err != nil {
		logger.Error(err, "failed to patch ingress with cert ARN")
		return ctrl.Result{}, err
	}

	logger.Info("Patched ingress with ACM cert ARN", "arn", certArn)
	return ctrl.Result{RequeueAfter: 12 * time.Hour}, nil
}

func (r *IngressReconciler) deleteCertificateForDomain(ctx context.Context, domain string) error {
	out, err := r.ACMClient.ListCertificates(ctx, &acm.ListCertificatesInput{
		CertificateStatuses: []acmtypes.CertificateStatus{
			acmtypes.CertificateStatusIssued,
			acmtypes.CertificateStatusPendingValidation,
		},
	})
	if err != nil {
		return err
	}

	for _, cert := range out.CertificateSummaryList {
		if strings.EqualFold(aws.ToString(cert.DomainName), domain) {
			_, err := r.ACMClient.DeleteCertificate(ctx, &acm.DeleteCertificateInput{
				CertificateArn: cert.CertificateArn,
			})
			return err
		}
	}

	return nil
}

func (r *IngressReconciler) ensureCertificate(ctx context.Context, domain string, cfg IngressConfig) (string, error) {
	if cfg.ReuseExisting {
		out, err := r.ACMClient.ListCertificates(ctx, &acm.ListCertificatesInput{
			CertificateStatuses: []acmtypes.CertificateStatus{
				acmtypes.CertificateStatusIssued,
				acmtypes.CertificateStatusPendingValidation,
			},
		})
		if err != nil {
			return "", err
		}
		for _, cert := range out.CertificateSummaryList {
			if strings.EqualFold(aws.ToString(cert.DomainName), domain) {
				return aws.ToString(cert.CertificateArn), nil
			}
		}
	}

	req := &acm.RequestCertificateInput{
		DomainName:       aws.String(domain),
		ValidationMethod: acmtypes.ValidationMethodDns,
		Tags: []acmtypes.Tag{
			{Key: aws.String("ManagedBy"), Value: aws.String("acm-manager")},
		},
	}

	if cfg.Wildcard {
		req.DomainName = aws.String("*." + domain)
	}

	if len(cfg.SANs) > 0 {
		req.SubjectAlternativeNames = cfg.SANs
	}

	resp, err := r.ACMClient.RequestCertificate(ctx, req)
	if err != nil {
		return "", err
	}

	certArn := aws.ToString(resp.CertificateArn)

	if err := r.createRoute53ValidationRecords(ctx, certArn, cfg.ZoneID); err != nil {
		logger := log.FromContext(ctx)
		logger.Error(err, "failed to create DNS validation records")
		return certArn, err
	}

	timeout := 10 * time.Minute
	interval := 15 * time.Second
	deadline := time.Now().Add(timeout)

	attempts := 0

	for {
		if time.Now().After(deadline) {
			return certArn, fmt.Errorf("certificate validation timed out: %s", certArn)
		}

		describe, err := r.ACMClient.DescribeCertificate(ctx, &acm.DescribeCertificateInput{
			CertificateArn: aws.String(certArn),
		})
		if err != nil {
			return certArn, err
		}

		status := describe.Certificate.Status

		attempts++
		if attempts%4 == 0 {
			logger := log.FromContext(ctx)
			logger.Info("Waiting for ACM certificate validation", "attempt", attempts, "certArn", certArn)
		}

		switch status {
		case acmtypes.CertificateStatusIssued:
			return certArn, nil
		case acmtypes.CertificateStatusFailed:
			return certArn, fmt.Errorf("certificate validation failed: %s", describe.Certificate.FailureReason)
		default:
			time.Sleep(interval)
		}
	}
}

func (r *IngressReconciler) createRoute53ValidationRecords(ctx context.Context, certArn string, zoneID string) error {
	describe, err := r.ACMClient.DescribeCertificate(ctx, &acm.DescribeCertificateInput{
		CertificateArn: aws.String(certArn),
	})
	if err != nil {
		return fmt.Errorf("failed to describe certificate: %w", err)
	}

	seen := make(map[string]bool)
	for _, option := range describe.Certificate.DomainValidationOptions {
		logger := log.FromContext(ctx)
		logger.Info("Processing domain validation option", "domain", aws.ToString(option.DomainName))

		record := option.ResourceRecord
		if record == nil {
			continue
		}

		key := fmt.Sprintf("%s|%s|%s", aws.ToString(record.Name), record.Type, aws.ToString(record.Value))
		if seen[key] {
			continue
		}
		seen[key] = true

		hostedZoneID := zoneID
		if hostedZoneID == "" {
			guessedZoneID, err := r.findMatchingHostedZone(ctx, aws.ToString(option.DomainName))
			if err != nil {
				return fmt.Errorf("failed to infer zone: %w", err)
			}
			hostedZoneID = guessedZoneID
		}

		logger.Info("Creating Route 53 validation record", "zone", hostedZoneID, "name", aws.ToString(record.Name), "type", record.Type, "value", aws.ToString(record.Value))

		change := &route53.ChangeResourceRecordSetsInput{
			HostedZoneId: aws.String(hostedZoneID),
			ChangeBatch: &route53types.ChangeBatch{
				Changes: []route53types.Change{
					{
						Action: route53types.ChangeActionUpsert,
						ResourceRecordSet: &route53types.ResourceRecordSet{
							Name: record.Name,
							Type: route53types.RRType(record.Type),
							TTL:  aws.Int64(300),
							ResourceRecords: []route53types.ResourceRecord{
								{Value: record.Value},
							},
						},
					},
				},
			},
		}

		_, err := r.Route53Client.ChangeResourceRecordSets(ctx, change)
		if err != nil {
			return fmt.Errorf("failed to create DNS validation record: %w", err)
		}
	}

	return nil
}

func (r *IngressReconciler) findMatchingHostedZone(ctx context.Context, domain string) (string, error) {
	list, err := r.Route53Client.ListHostedZones(ctx, &route53.ListHostedZonesInput{})
	if err != nil {
		return "", err
	}

	var matchedZoneID string
	var longestMatchLen int

	for _, zone := range list.HostedZones {
		zoneName := strings.TrimSuffix(aws.ToString(zone.Name), ".")
		if strings.HasSuffix(domain, zoneName) && len(zoneName) > longestMatchLen {
			matchedZoneID = aws.ToString(zone.Id)
			longestMatchLen = len(zoneName)
		}
	}

	if matchedZoneID == "" {
		return "", fmt.Errorf("no matching hosted zone found for domain: %s", domain)
	}

	return strings.TrimPrefix(matchedZoneID, "/hostedzone/"), nil
}

func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return err
	}

	r.ACMClient = acm.NewFromConfig(cfg)
	r.Route53Client = route53.NewFromConfig(cfg)

	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(r)
}
