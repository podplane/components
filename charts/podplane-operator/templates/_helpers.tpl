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

{{- define "podplane-operator.spcRestrictionPolicyName" -}}
{{ include "podplane-operator.namePrefix" . }}-spc-restriction-vap
{{- end -}}

{{- define "podplane-operator.spcRestrictionBindingName" -}}
{{ include "podplane-operator.namePrefix" . }}-spc-restriction-vapb
{{- end -}}

{{- define "podplane-operator.configJSON" -}}
{{- $valuesConfig := .Values.podplane.operator.config | default dict -}}
{{- $clusterValues := $valuesConfig.cluster | default dict -}}
{{- $secretsValues := $valuesConfig.secrets | default dict -}}
{{- $workloadValues := $valuesConfig.workloadCertificates | default dict -}}
{{- $ingressValues := $valuesConfig.ingressCertificates | default dict -}}
{{- $registryAuthEnabled := eq (include "podplane-operator.registryAuth.enabled" .) "true" -}}
{{- $clusterID := $clusterValues.id -}}
{{- $_ := required "podplane.operator.config.cluster.id is required" $clusterID -}}
{{- $oidcValues := $clusterValues.oidc | default dict -}}
{{- if $registryAuthEnabled -}}{{- $_ := required "podplane.operator.config.cluster.oidc.issuerURL is required when registry auth is enabled" $oidcValues.issuerURL -}}{{- end -}}
{{- $config := dict
  "cluster" (dict "id" $clusterID "oidc" (dict "client_id" (default $clusterID $oidcValues.clientID)))
  "secrets" (dict "key_rotation" (default "6h" $secretsValues.keyRotation) "allow_sync_to_kubernetes_secrets" (default false $secretsValues.allowSyncToKubernetesSecrets) "default_provider" (default "" $secretsValues.defaultProvider) "providers" dict)
  "registry" (dict "auth" (dict "enabled" $registryAuthEnabled))
-}}
{{- $spiffeValues := $clusterValues.spiffe | default dict -}}
{{- $_ := required "podplane.operator.config.cluster.spiffe.trustDomain is required" $spiffeValues.trustDomain -}}
{{- $_ := set (get $config "cluster") "spiffe" (dict "trust_domain" $spiffeValues.trustDomain) -}}
{{- $_ := set $config "certificates" (dict
  "ca_path" ($workloadValues.caPath | default "/var/run/podplane/certificates/workload-ca-key")
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
{{- toPrettyJson $config -}}
{{- end -}}

{{- define "podplane-operator.registryAuth.enabled" -}}
{{- $valuesConfig := .Values.podplane.operator.config | default dict -}}
{{- dig "registry" "auth" "enabled" false $valuesConfig -}}
{{- end -}}
