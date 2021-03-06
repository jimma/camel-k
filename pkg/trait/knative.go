/*
Licensed to the Apache Software Foundation (ASF) under one or more
contributor license agreements.  See the NOTICE file distributed with
this work for additional information regarding copyright ownership.
The ASF licenses this file to You under the Apache License, Version 2.0
(the "License"); you may not use this file except in compliance with
the License.  You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package trait

import (
	"net/url"
	"strings"

	"github.com/apache/camel-k/pkg/apis/camel/v1alpha1"
	knativeapi "github.com/apache/camel-k/pkg/apis/camel/v1alpha1/knative"
	"github.com/apache/camel-k/pkg/metadata"
	"github.com/apache/camel-k/pkg/util/envvar"
	knativeutil "github.com/apache/camel-k/pkg/util/knative"
	"github.com/apache/camel-k/pkg/util/kubernetes"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	eventing "knative.dev/eventing/pkg/apis/eventing/v1alpha1"
	messaging "knative.dev/eventing/pkg/apis/messaging/v1alpha1"
	serving "knative.dev/serving/pkg/apis/serving/v1beta1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type knativeTrait struct {
	BaseTrait            `property:",squash"`
	Configuration        string `property:"configuration"`
	ChannelSources       string `property:"channel-sources"`
	ChannelSinks         string `property:"channel-sinks"`
	EndpointSources      string `property:"endpoint-sources"`
	EndpointSinks        string `property:"endpoint-sinks"`
	EventSources         string `property:"event-sources"`
	EventSinks           string `property:"event-sinks"`
	FilterSourceChannels *bool  `property:"filter-source-channels"`
	Knative08CompatMode  *bool  `property:"knative-08-compat-mode"`
	Auto                 *bool  `property:"auto"`
}

const (
	knativeHistoryHeader = "ce-knativehistory"
)

func newKnativeTrait() *knativeTrait {
	t := &knativeTrait{
		BaseTrait: newBaseTrait("knative"),
	}

	return t
}

func (t *knativeTrait) Configure(e *Environment) (bool, error) {
	if t.Enabled != nil && !*t.Enabled {
		return false, nil
	}

	if !e.IntegrationInPhase(v1alpha1.IntegrationPhaseDeploying) {
		return false, nil
	}

	if t.Auto == nil || *t.Auto {
		if t.ChannelSources == "" {
			items := make([]string, 0)

			metadata.Each(e.CamelCatalog, e.Integration.Spec.Sources, func(_ int, meta metadata.IntegrationMetadata) bool {
				items = append(items, knativeutil.FilterURIs(meta.FromURIs, knativeapi.CamelServiceTypeChannel)...)
				return true
			})

			t.ChannelSources = strings.Join(items, ",")
		}
		if t.ChannelSinks == "" {
			items := make([]string, 0)

			metadata.Each(e.CamelCatalog, e.Integration.Spec.Sources, func(_ int, meta metadata.IntegrationMetadata) bool {
				items = append(items, knativeutil.FilterURIs(meta.ToURIs, knativeapi.CamelServiceTypeChannel)...)
				return true
			})

			t.ChannelSinks = strings.Join(items, ",")
		}
		if t.EndpointSources == "" {
			items := make([]string, 0)

			metadata.Each(e.CamelCatalog, e.Integration.Spec.Sources, func(_ int, meta metadata.IntegrationMetadata) bool {
				items = append(items, knativeutil.FilterURIs(meta.FromURIs, knativeapi.CamelServiceTypeEndpoint)...)
				return true
			})

			t.EndpointSources = strings.Join(items, ",")
		}
		if t.EndpointSinks == "" {
			items := make([]string, 0)

			metadata.Each(e.CamelCatalog, e.Integration.Spec.Sources, func(_ int, meta metadata.IntegrationMetadata) bool {
				items = append(items, knativeutil.FilterURIs(meta.ToURIs, knativeapi.CamelServiceTypeEndpoint)...)
				return true
			})

			t.EndpointSinks = strings.Join(items, ",")
		}
		if t.EventSources == "" {
			items := make([]string, 0)

			metadata.Each(e.CamelCatalog, e.Integration.Spec.Sources, func(_ int, meta metadata.IntegrationMetadata) bool {
				items = append(items, knativeutil.FilterURIs(meta.FromURIs, knativeapi.CamelServiceTypeEvent)...)
				return true
			})

			t.EventSources = strings.Join(items, ",")
		}
		if t.EventSinks == "" {
			items := make([]string, 0)

			metadata.Each(e.CamelCatalog, e.Integration.Spec.Sources, func(_ int, meta metadata.IntegrationMetadata) bool {
				items = append(items, knativeutil.FilterURIs(meta.ToURIs, knativeapi.CamelServiceTypeEvent)...)
				return true
			})

			t.EventSinks = strings.Join(items, ",")
		}
		if len(strings.Split(t.ChannelSources, ",")) > 1 {
			// Always filter channels when the integration subscribes to more than one
			// Using Knative experimental header: https://github.com/knative/eventing/blob/7df0cc56c28d58223ff25d5ddfb487fa8c29a004/pkg/provisioners/message.go#L28
			// TODO: filter automatically all source channels when the feature becomes stable
			filter := true
			t.FilterSourceChannels = &filter
		}

		if t.Knative08CompatMode == nil {
			compat, err := t.shouldUseKnative08CompatMode(e.Integration.Namespace)
			if err != nil {
				return false, err
			}
			t.Knative08CompatMode = &compat
		}
	}

	return true, nil
}

func (t *knativeTrait) Apply(e *Environment) error {
	env := knativeapi.NewCamelEnvironment()
	if t.Configuration != "" {
		if err := env.Deserialize(t.Configuration); err != nil {
			return err
		}
	}

	if err := t.configureChannels(e, &env); err != nil {
		return err
	}
	if err := t.configureEndpoints(e, &env); err != nil {
		return err
	}
	if err := t.configureEvents(e, &env); err != nil {
		return err
	}

	conf, err := env.Serialize()
	if err != nil {
		return errors.Wrap(err, "unable to fetch environment configuration")
	}

	envvar.SetVal(&e.EnvVars, "CAMEL_KNATIVE_CONFIGURATION", conf)

	return nil
}

func (t *knativeTrait) configureChannels(e *Environment, env *knativeapi.CamelEnvironment) error {
	// Sources
	err := t.ifServiceMissingDo(e, env, t.ChannelSources, knativeapi.CamelServiceTypeChannel, knativeapi.CamelEndpointKindSource,
		func(ref *v1.ObjectReference, loc *url.URL, serviceURI string) error {
			meta := map[string]string{
				knativeapi.CamelMetaServicePath:       "/",
				knativeapi.CamelMetaEndpointKind:      string(knativeapi.CamelEndpointKindSource),
				knativeapi.CamelMetaKnativeAPIVersion: ref.APIVersion,
				knativeapi.CamelMetaKnativeKind:       ref.Kind,
			}
			if t.FilterSourceChannels != nil && *t.FilterSourceChannels {
				meta[knativeapi.CamelMetaFilterPrefix+knativeHistoryHeader] = loc.Host
			}
			svc := knativeapi.CamelServiceDefinition{
				Name:        ref.Name,
				Host:        "0.0.0.0",
				Port:        8080,
				ServiceType: knativeapi.CamelServiceTypeChannel,
				Metadata:    meta,
			}
			env.Services = append(env.Services, svc)

			if err := t.createSubscription(e, ref); err != nil {
				return err
			}
			return nil
		})
	if err != nil {
		return err
	}

	// Sinks
	err = t.ifServiceMissingDo(e, env, t.ChannelSinks, knativeapi.CamelServiceTypeChannel, knativeapi.CamelEndpointKindSink,
		func(ref *v1.ObjectReference, loc *url.URL, serviceURI string) error {
			svc, err := knativeapi.BuildCamelServiceDefinition(ref.Name, knativeapi.CamelEndpointKindSink,
				knativeapi.CamelServiceTypeChannel, *loc, ref.APIVersion, ref.Kind)
			if err != nil {
				return err
			}
			env.Services = append(env.Services, svc)
			return nil
		})
	if err != nil {
		return err
	}

	return nil
}

func (t *knativeTrait) createSubscription(e *Environment, ref *v1.ObjectReference) error {
	compat := t.Knative08CompatMode != nil && *t.Knative08CompatMode
	sub := knativeutil.CreateSubscription(*ref, e.Integration.Name, compat)
	e.Resources.Add(sub)
	return nil
}

func (t *knativeTrait) configureEndpoints(e *Environment, env *knativeapi.CamelEnvironment) error {
	// Sources
	serviceSources := t.extractServices(t.EndpointSources, knativeapi.CamelServiceTypeEndpoint)
	for _, endpoint := range serviceSources {
		ref, err := knativeutil.ExtractObjectReference(endpoint)
		if err != nil {
			return err
		}
		if env.ContainsService(endpoint, knativeapi.CamelEndpointKindSource, knativeapi.CamelServiceTypeEndpoint,
			serving.SchemeGroupVersion.String(), "Service") {
			continue
		}
		svc := knativeapi.CamelServiceDefinition{
			Name:        ref.Name,
			Host:        "0.0.0.0",
			Port:        8080,
			ServiceType: knativeapi.CamelServiceTypeEndpoint,
			Metadata: map[string]string{
				knativeapi.CamelMetaServicePath:       "/",
				knativeapi.CamelMetaEndpointKind:      string(knativeapi.CamelEndpointKindSource),
				knativeapi.CamelMetaKnativeAPIVersion: serving.SchemeGroupVersion.String(),
				knativeapi.CamelMetaKnativeKind:       "Service",
			},
		}
		env.Services = append(env.Services, svc)
	}

	// Sinks
	err := t.ifServiceMissingDo(e, env, t.EndpointSinks, knativeapi.CamelServiceTypeEndpoint, knativeapi.CamelEndpointKindSink,
		func(ref *v1.ObjectReference, loc *url.URL, serviceURI string) error {
			svc, err := knativeapi.BuildCamelServiceDefinition(ref.Name, knativeapi.CamelEndpointKindSink,
				knativeapi.CamelServiceTypeEndpoint, *loc, ref.APIVersion, ref.Kind)
			if err != nil {
				return err
			}
			env.Services = append(env.Services, svc)
			return nil
		})
	if err != nil {
		return err
	}

	return nil
}

func (t *knativeTrait) configureEvents(e *Environment, env *knativeapi.CamelEnvironment) error {
	// Sources
	err := t.withServiceDo(false, e, env, t.EventSources, knativeapi.CamelServiceTypeEvent, knativeapi.CamelEndpointKindSource,
		func(ref *v1.ObjectReference, loc *url.URL, serviceURI string) error {
			// Iterate over all, without skipping duplicates
			eventType := knativeutil.ExtractEventType(serviceURI)
			t.createTrigger(e, ref, eventType)

			if !env.ContainsService(ref.Name, knativeapi.CamelEndpointKindSource, knativeapi.CamelServiceTypeEvent, ref.APIVersion, ref.Kind) {
				svc := knativeapi.CamelServiceDefinition{
					Name:        ref.Name,
					Host:        "0.0.0.0",
					Port:        8080,
					ServiceType: knativeapi.CamelServiceTypeEvent,
					Metadata: map[string]string{
						knativeapi.CamelMetaServicePath:       "/",
						knativeapi.CamelMetaEndpointKind:      string(knativeapi.CamelEndpointKindSource),
						knativeapi.CamelMetaKnativeAPIVersion: ref.APIVersion,
						knativeapi.CamelMetaKnativeKind:       ref.Kind,
					},
				}
				env.Services = append(env.Services, svc)
			}
			return nil
		})
	if err != nil {
		return err
	}

	// Sinks
	err = t.ifServiceMissingDo(e, env, t.EventSinks, knativeapi.CamelServiceTypeEvent, knativeapi.CamelEndpointKindSink,
		func(ref *v1.ObjectReference, loc *url.URL, serviceURI string) error {
			svc, err := knativeapi.BuildCamelServiceDefinition(ref.Name, knativeapi.CamelEndpointKindSink,
				knativeapi.CamelServiceTypeEvent, *loc, ref.APIVersion, ref.Kind)
			if err != nil {
				return err
			}
			env.Services = append(env.Services, svc)
			return nil
		})
	if err != nil {
		return err
	}

	return nil
}

func (t *knativeTrait) createTrigger(e *Environment, ref *v1.ObjectReference, eventType string) {
	// TODO extend to additional filters too, to filter them at source and not at destination
	found := e.Resources.HasKnativeTrigger(func(trigger *eventing.Trigger) bool {
		return trigger.Spec.Broker == ref.Name &&
			trigger.Spec.Filter != nil &&
			trigger.Spec.Filter.Attributes != nil &&
			(*trigger.Spec.Filter.Attributes)["type"] == eventType
	})
	if !found {
		trigger := knativeutil.CreateTrigger(*ref, e.Integration.Name, eventType)
		e.Resources.Add(trigger)
	}
}

func (t *knativeTrait) ifServiceMissingDo(
	e *Environment,
	env *knativeapi.CamelEnvironment,
	serviceURIsAsString string,
	serviceType knativeapi.CamelServiceType,
	endpointKind knativeapi.CamelEndpointKind,
	gen func(ref *v1.ObjectReference, url *url.URL, serviceURI string) error) error {
	return t.withServiceDo(true, e, env, serviceURIsAsString, serviceType, endpointKind, gen)
}

func (t *knativeTrait) withServiceDo(
	skipDuplicates bool,
	e *Environment,
	env *knativeapi.CamelEnvironment,
	serviceURIsAsString string,
	serviceType knativeapi.CamelServiceType,
	endpointKind knativeapi.CamelEndpointKind,
	gen func(ref *v1.ObjectReference, url *url.URL, serviceURI string) error) error {

	serviceURIs := t.extractServices(serviceURIsAsString, serviceType)
	for _, serviceURI := range serviceURIs {
		ref, err := knativeutil.ExtractObjectReference(serviceURI)
		if err != nil {
			return err
		}
		if skipDuplicates && env.ContainsService(ref.Name, endpointKind, serviceType, ref.APIVersion, ref.Kind) {
			continue
		}
		possibleRefs := knativeutil.FillMissingReferenceData(serviceType, ref)
		actualRef, err := knativeutil.GetAddressableReference(t.ctx, t.client, possibleRefs, e.Integration.Namespace, ref.Name)
		if err != nil && k8serrors.IsNotFound(err) {
			return errors.Errorf("cannot find %s %s", serviceType, ref.Name)
		} else if err != nil {
			return errors.Wrapf(err, "error looking up %s %s", serviceType, ref.Name)
		}
		targetURL, err := knativeutil.GetSinkURL(t.ctx, t.client, actualRef, e.Integration.Namespace)
		if err != nil {
			return errors.Wrapf(err, "cannot determine address of %s %s", string(serviceType), ref.Name)
		}
		t.L.Infof("Found URL for %s: %s", string(serviceType), targetURL.String())
		err = gen(actualRef, targetURL, serviceURI)
		if err != nil {
			return errors.Wrapf(err, "unexpected error while executing handler for %s %s", string(serviceType), ref.Name)
		}
	}
	return nil
}

func (t *knativeTrait) extractServices(names string, serviceType knativeapi.CamelServiceType) []string {
	answer := make([]string, 0)
	for _, item := range strings.Split(names, ",") {
		i := strings.Trim(item, " \t\"")
		if i != "" {
			i = knativeutil.NormalizeToURI(serviceType, i)
			answer = append(answer, i)
		}
	}
	return answer
}

func (t *knativeTrait) shouldUseKnative08CompatMode(namespace string) (bool, error) {
	lst := messaging.SubscriptionList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Subscription",
			APIVersion: messaging.SchemeGroupVersion.String(),
		},
	}
	err := t.client.List(t.ctx, &lst, k8sclient.InNamespace(namespace))
	if err != nil && kubernetes.IsUnknownAPIError(err) {
		return true, nil
	}
	return false, err
}
