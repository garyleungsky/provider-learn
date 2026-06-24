/*
Copyright 2025 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package instance

import (
	"context"

	"github.com/crossplane/crossplane-runtime/v2/pkg/feature"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	xpv1 "github.com/crossplane/crossplane-runtime/v2/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/event"
	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/crossplane/crossplane-runtime/v2/pkg/statemetrics"

	v1alpha1 "github.com/garyleungsky/provider-learn/apis/database/v1alpha1"
	apisv1alpha1 "github.com/garyleungsky/provider-learn/apis/v1alpha1"
)

const (
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCPC       = "cannot get ClusterProviderConfig"
	errGetCreds     = "cannot get credentials"

	errNewClient = "cannot create API client"

	errGet    = "cannot get instance"
	errCreate = "cannot create instance"
	errUpdate = "cannot update instance"
	errDelete = "cannot delete instance"
)

// SetupGated adds a controller that reconciles Instance managed resources with safe-start support.
func SetupGated(mgr ctrl.Manager, o controller.Options) error {
	o.Gate.Register(func() {
		if err := Setup(mgr, o); err != nil {
			panic(errors.Wrap(err, "cannot setup Instance controller"))
		}
	}, v1alpha1.InstanceGroupVersionKind)
	return nil
}

func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.InstanceGroupKind)

	opts := []managed.ReconcilerOption{
		managed.WithTypedExternalConnector[*v1alpha1.Instance](&connector{
			kube:        mgr.GetClient(),
			usage:       resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newClientFn: newAPIClient}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
	}

	if o.Features.Enabled(feature.EnableBetaManagementPolicies) {
		opts = append(opts, managed.WithManagementPolicies())
	}

	if o.Features.Enabled(feature.EnableAlphaChangeLogs) {
		opts = append(opts, managed.WithChangeLogger(o.ChangeLogOptions.ChangeLogger))
	}

	if o.MetricOptions != nil {
		opts = append(opts, managed.WithMetricRecorder(o.MetricOptions.MRMetrics))
	}

	if o.MetricOptions != nil && o.MetricOptions.MRStateMetrics != nil {
		stateMetricsRecorder := statemetrics.NewMRStateRecorder(
			mgr.GetClient(), o.Logger, o.MetricOptions.MRStateMetrics, &v1alpha1.InstanceList{}, o.MetricOptions.PollStateMetricInterval,
		)
		if err := mgr.Add(stateMetricsRecorder); err != nil {
			return errors.Wrap(err, "cannot register MR state metrics recorder for kind v1alpha1.InstanceList")
		}
	}

	r := managed.NewReconciler(mgr, resource.ManagedKind(v1alpha1.InstanceGroupVersionKind), opts...)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		WithEventFilter(resource.DesiredStateChanged()).
		For(&v1alpha1.Instance{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube        client.Client
	usage       *resource.ProviderConfigUsageTracker
	newClientFn func(creds []byte) (*apiClient, error)
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, cr *v1alpha1.Instance) (managed.TypedExternalClient[*v1alpha1.Instance], error) {
	if err := c.usage.Track(ctx, cr); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	var cd apisv1alpha1.ProviderCredentials

	ref := cr.GetProviderConfigReference()

	switch ref.Kind {
	case "ProviderConfig":
		pc := &apisv1alpha1.ProviderConfig{}
		if err := c.kube.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: cr.GetNamespace()}, pc); err != nil {
			return nil, errors.Wrap(err, errGetPC)
		}
		cd = pc.Spec.Credentials
	case "ClusterProviderConfig":
		cpc := &apisv1alpha1.ClusterProviderConfig{}
		if err := c.kube.Get(ctx, types.NamespacedName{Name: ref.Name}, cpc); err != nil {
			return nil, errors.Wrap(err, errGetCPC)
		}
		cd = cpc.Spec.Credentials
	default:
		return nil, errors.Errorf("unsupported provider config kind: %s", ref.Kind)
	}
	data, err := resource.CommonCredentialExtractor(ctx, cd.Source, c.kube, cd.CommonCredentialSelectors)
	if err != nil {
		return nil, errors.Wrap(err, errGetCreds)
	}

	client, err := c.newClientFn(data)
	if err != nil {
		return nil, errors.Wrap(err, errNewClient)
	}

	return &external{client: client}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	// client talks to the external instance API over HTTP.
	client *apiClient
}

// connectionDetails returns the connection secret published for an instance.
func connectionDetails(observableField string) managed.ConnectionDetails {
	return managed.ConnectionDetails{"observableField": []byte(observableField)}
}

func (c *external) Observe(ctx context.Context, cr *v1alpha1.Instance) (managed.ExternalObservation, error) {
	// The external-name annotation (defaulted to metadata.name) is the
	// instance's identity in the external API.
	name := meta.GetExternalName(cr)
	if name == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	observed, found, err := c.client.Get(ctx, name)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errGet)
	}
	if !found {
		// A 404 tells the reconciler to call Create.
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	// Reflect the observed external state into status.atProvider.
	cr.Status.AtProvider.ConfigurableField = observed.ConfigurableField
	cr.Status.AtProvider.ObservableField = observed.ObservableField
	cr.SetConditions(xpv1.Available())

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  observed.ConfigurableField == cr.Spec.ForProvider.ConfigurableField,
		ConnectionDetails: connectionDetails(observed.ObservableField),
	}, nil
}

func (c *external) Create(ctx context.Context, cr *v1alpha1.Instance) (managed.ExternalCreation, error) {
	cr.SetConditions(xpv1.Creating())

	created, err := c.client.Create(ctx, apiInstance{
		Name:              meta.GetExternalName(cr),
		ConfigurableField: cr.Spec.ForProvider.ConfigurableField,
	})
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreate)
	}

	return managed.ExternalCreation{ConnectionDetails: connectionDetails(created.ObservableField)}, nil
}

func (c *external) Update(ctx context.Context, cr *v1alpha1.Instance) (managed.ExternalUpdate, error) {
	updated, err := c.client.Update(ctx, meta.GetExternalName(cr), cr.Spec.ForProvider.ConfigurableField)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdate)
	}

	return managed.ExternalUpdate{ConnectionDetails: connectionDetails(updated.ObservableField)}, nil
}

func (c *external) Delete(ctx context.Context, cr *v1alpha1.Instance) (managed.ExternalDelete, error) {
	cr.SetConditions(xpv1.Deleting())

	if err := c.client.Delete(ctx, meta.GetExternalName(cr)); err != nil {
		return managed.ExternalDelete{}, errors.Wrap(err, errDelete)
	}

	return managed.ExternalDelete{}, nil
}

func (c *external) Disconnect(_ context.Context) error {
	return nil
}
