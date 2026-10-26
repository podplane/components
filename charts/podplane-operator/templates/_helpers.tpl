{{/*
Podplane <https://podplane.dev>
Copyright The Podplane Authors
SPDX-License-Identifier: Apache-2.0
*/}}
{{- define "podplane-operator.name" -}}
podplane-operator
{{- end -}}

{{- define "podplane-operator.namePrefix" -}}
{{- .Values.podplane.operator.namePrefix | default "platform-podplane-operator" -}}
{{- end -}}

{{- define "podplane-operator.labels" -}}
app.kubernetes.io/name: {{ include "podplane-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: operator
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "podplane-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "podplane-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: operator
{{- end -}}

{{- define "podplane-operator.rbac.defaultRoles.namePrefix" -}}
{{- .Values.podplane.operator.rbac.defaultRoles.namePrefix | default "platform-podplane-secrets" -}}
{{- end -}}

{{- define "podplane-operator.serviceAccountName" -}}
{{- .Values.podplane.operator.serviceAccountName | default "platform-podplane-operator" -}}
{{- end -}}

{{- define "podplane-operator.configJSON" -}}
{{- $valuesConfig := .Values.podplane.operator.config | default dict -}}
{{- $clusterValues := $valuesConfig.cluster | default dict -}}
{{- $secretsValues := $valuesConfig.secrets | default dict -}}
{{- $workloadValues := $valuesConfig.workloadCertificates | default dict -}}
{{- $ingressValues := $valuesConfig.ingressCertificates | default dict -}}
{{- $clusterID := $clusterValues.id -}}
{{- $_ := required "podplane.operator.config.cluster.id is required" $clusterID -}}
{{- $oidcValues := $clusterValues.oidc | default dict -}}
{{- $config := dict
  "cluster" (dict "id" $clusterID "oidc" (dict "client_id" (default $clusterID $oidcValues.clientID)))
  "secrets" (dict "key_rotation" (default "6h" $secretsValues.keyRotation) "allow_sync_to_kubernetes_secrets" (default false $secretsValues.allowSyncToKubernetesSecrets) "default_provider" (default "" $secretsValues.defaultProvider) "providers" dict)
-}}
{{- $spiffeValues := $clusterValues.spiffe | default dict -}}
{{- $_ := required "podplane.operator.config.cluster.spiffe.trustDomain is required" $spiffeValues.trustDomain -}}
{{- $_ := set (get $config "cluster") "spiffe" (dict "trust_domain" $spiffeValues.trustDomain) -}}
{{- $_ := set $config "certificates" (dict
  "ca_path" ($workloadValues.caPath | default "/var/run/podplane/certificates/workload-ca-key")
  "trust_bundle_sync" (include "podplane-operator.trustBundleSync" . | fromJson)
) -}}
{{- $oidc := get (get $config "cluster") "oidc" -}}
{{- with $oidcValues.issuerURL }}{{- $_ := set $oidc "issuer_url" . -}}{{- end -}}
{{- with $oidcValues.usernameClaim }}{{- $_ := set $oidc "username_claim" . -}}{{- end -}}
{{- with $oidcValues.groupsClaim }}{{- $_ := set $oidc "groups_claim" . -}}{{- end -}}
{{- $providers := get (get $config "secrets") "providers" -}}
{{- range $name, $provider := ($secretsValues.providers | default dict) -}}
{{- $entry := dict -}}
{{- range $key, $value := $provider -}}
{{- $_ := set $entry (snakecase $key) $value -}}
{{- end -}}
{{- $_ := set $providers $name $entry -}}
{{- end -}}
{{- if $ingressValues.domains -}}
{{- $ingress := dict
  "provider" (default "" $ingressValues.provider)
  "key_prefix" (default "" $ingressValues.keyPrefix)
  "check_interval" (default "1h" $ingressValues.checkInterval)
  "renew_before" (default "720h" $ingressValues.renewBefore)
  "domains" dict
-}}
{{- with $ingressValues.acme -}}
{{- $_ := set $ingress "acme" (dict "server" .server "email" .email) -}}
{{- end -}}
{{- $domains := get $ingress "domains" -}}
{{- range $name, $domain := $ingressValues.domains -}}
{{- $entry := dict -}}
{{- with $domain.dnsProvider -}}
{{- $_ := set $entry "dns_provider" (dict "kind" .kind "region" (default "" .region) "hosted_zone_id" (default "" .hostedZoneID) "role_arn" (default "" .roleARN)) -}}
{{- end -}}
{{- $_ := set $domains $name $entry -}}
{{- end -}}
{{- $_ := set $config "ingress_certificates" $ingress -}}
{{- end -}}
{{- $_ := set $config "rbac" (dict "namespace_group_bindings" (dig "rbac" "namespaceGroupBindings" false $valuesConfig)) -}}
{{- toPrettyJson $config -}}
{{- end -}}

{{/* Validate and translate trust-bundle destinations once for config and RBAC. */}}
{{- define "podplane-operator.trustBundleSync" -}}
{{- $workload := .Values.podplane.operator.config.workloadCertificates | default dict -}}
{{- $sync := dict -}}
{{- if hasKey $workload "trustBundleSync" -}}
{{- $sync = $workload.trustBundleSync -}}
{{- end -}}
{{- if not (kindIs "map" $sync) -}}
{{- fail "workloadCertificates.trustBundleSync must be an object" -}}
{{- end -}}
{{- $create := false -}}
{{- if hasKey $sync "createIfMissing" -}}
{{- if not (kindIs "bool" $sync.createIfMissing) -}}
{{- fail "workloadCertificates.trustBundleSync.createIfMissing must be a boolean" -}}
{{- end -}}
{{- $create = $sync.createIfMissing -}}
{{- end -}}
{{- $resources := list -}}
{{- if hasKey $sync "resources" -}}
{{- if not (kindIs "slice" $sync.resources) -}}
{{- fail "workloadCertificates.trustBundleSync.resources must be a list" -}}
{{- end -}}
{{- $resources = $sync.resources -}}
{{- end -}}
{{- $entries := list -}}
{{- $seen := dict -}}
{{- range $index, $resource := $resources -}}
{{- $path := printf "workloadCertificates.trustBundleSync.resources[%d]" $index -}}
{{- if not (kindIs "map" $resource) -}}
{{- fail (printf "%s must be an object" $path) -}}
{{- end -}}
{{- range $field := list "kind" "namespace" "name" "key" -}}
{{- if not (kindIs "string" (get $resource $field)) -}}
{{- fail (printf "%s.%s must be a string" $path $field) -}}
{{- end -}}
{{- end -}}
{{- if not (has $resource.kind (list "Secret" "ConfigMap")) -}}
{{- fail (printf "%s.kind must be Secret or ConfigMap" $path) -}}
{{- end -}}
{{- if or (gt (len $resource.namespace) 63) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" $resource.namespace)) -}}
{{- fail (printf "%s.namespace must be a DNS label of at most 63 characters" $path) -}}
{{- end -}}
{{- if or (gt (len $resource.name) 253) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$" $resource.name)) -}}
{{- fail (printf "%s.name must be a DNS subdomain of at most 253 characters" $path) -}}
{{- end -}}
{{- if or (gt (len $resource.key) 253) (not (regexMatch "^[-._a-zA-Z0-9]+$" $resource.key)) (eq $resource.key ".") (hasPrefix ".." $resource.key) -}}
{{- fail (printf "%s.key must be a valid Secret or ConfigMap data key" $path) -}}
{{- end -}}
{{- $destination := printf "%s/%s/%s/%s" $resource.kind $resource.namespace $resource.name $resource.key -}}
{{- if hasKey $seen $destination -}}
{{- fail (printf "%s duplicates trustBundleSync destination %s" $path $destination) -}}
{{- end -}}
{{- $_ := set $seen $destination true -}}
{{- $entry := dict "kind" $resource.kind "namespace" $resource.namespace "name" $resource.name "key" $resource.key -}}
{{- if hasKey $resource "createIfMissing" -}}
{{- if not (kindIs "bool" $resource.createIfMissing) -}}
{{- fail (printf "%s.createIfMissing must be a boolean" $path) -}}
{{- end -}}
{{- $_ := set $entry "create_if_missing" $resource.createIfMissing -}}
{{- end -}}
{{- $entries = append $entries $entry -}}
{{- end -}}
{{- toJson (dict "create_if_missing" $create "resources" $entries) -}}
{{- end -}}
