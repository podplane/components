{{/*
Podplane <https://podplane.dev>
Copyright The Podplane Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{- define "podplane.platform.releaseName" -}}
{{- $name := . -}}
{{- if hasPrefix "platform-" $name -}}{{ $name }}{{- else -}}platform-{{ $name }}{{- end -}}
{{- end -}}

{{- define "podplane.platform.mirrorValues" -}}
{{- $name := .name -}}
{{- $hostname := .hostname -}}
{{- if eq $name "agent-sandbox" -}}
image:
  repository: {{ printf "%s/registry.k8s.io/agent-sandbox/agent-sandbox-controller" $hostname | quote }}
{{- else if eq $name "cert-manager" -}}
cert-manager:
  image:
    repository: {{ printf "%s/quay.io/jetstack/cert-manager-controller" $hostname | quote }}
  webhook:
    image:
      repository: {{ printf "%s/quay.io/jetstack/cert-manager-webhook" $hostname | quote }}
  cainjector:
    image:
      repository: {{ printf "%s/quay.io/jetstack/cert-manager-cainjector" $hostname | quote }}
cert-manager-approver-policy:
  image:
    repository: {{ printf "%s/quay.io/jetstack/cert-manager-approver-policy" $hostname | quote }}
{{- else if eq $name "platform-certs" -}}
platform:
  certs:
    secretSync:
      image:
        repository: {{ printf "%s/docker.io/library/golang" $hostname | quote }}
{{- else if eq $name "cilium" -}}
cilium:
  image:
    repository: {{ printf "%s/quay.io/cilium/cilium" $hostname | quote }}
  operator:
    image:
      repository: {{ printf "%s/quay.io/cilium/operator" $hostname | quote }}
{{- else if eq $name "coredns" -}}
coredns:
  image:
    repository: {{ printf "%s/docker.io/coredns/coredns" $hostname | quote }}
{{- else if eq $name "csi-aws-ebs" -}}
aws-ebs-csi-driver:
  image:
    repository: {{ printf "%s/public.ecr.aws/ebs-csi-driver/aws-ebs-csi-driver" $hostname | quote }}
  sidecars:
    provisioner:
      image:
        repository: {{ printf "%s/public.ecr.aws/csi-components/csi-provisioner" $hostname | quote }}
    attacher:
      image:
        repository: {{ printf "%s/public.ecr.aws/csi-components/csi-attacher" $hostname | quote }}
    livenessProbe:
      image:
        repository: {{ printf "%s/public.ecr.aws/csi-components/livenessprobe" $hostname | quote }}
    resizer:
      image:
        repository: {{ printf "%s/public.ecr.aws/csi-components/csi-resizer" $hostname | quote }}
    nodeDriverRegistrar:
      image:
        repository: {{ printf "%s/public.ecr.aws/csi-components/csi-node-driver-registrar" $hostname | quote }}
{{- else if eq $name "podplane-operator" -}}
podplane:
  operator:
    image:
      repository: {{ printf "%s/ghcr.io/podplane/operator" $hostname | quote }}
{{- else if eq $name "secrets-store-csi-driver" -}}
secrets-store-csi-driver:
  linux:
    image:
      repository: {{ printf "%s/registry.k8s.io/csi-secrets-store/driver" $hostname | quote }}
    crds:
      image:
        repository: {{ printf "%s/registry.k8s.io/csi-secrets-store/driver-crds" $hostname | quote }}
    registrarImage:
      repository: {{ printf "%s/registry.k8s.io/sig-storage/csi-node-driver-registrar" $hostname | quote }}
    livenessProbeImage:
      repository: {{ printf "%s/registry.k8s.io/sig-storage/livenessprobe" $hostname | quote }}
{{- else if eq $name "secrets-store-csi-provider-aws" -}}
secrets-store-csi-driver-provider-aws:
  image:
    repository: {{ printf "%s/public.ecr.aws/aws-secrets-manager/secrets-store-csi-driver-provider-aws" $hostname | quote }}
{{- else if eq $name "secrets-store-csi-provider-gcp" -}}
gcp:
  image:
    repository: {{ printf "%s/us-docker.pkg.dev/secretmanager-csi/secrets-store-csi-driver-provider-gcp/plugin" $hostname | quote }}
  initImage:
    repository: {{ printf "%s/docker.io/library/busybox" $hostname | quote }}
{{- else if eq $name "secrets-store-csi-provider-vault" -}}
vault:
  csi:
    image:
      repository: {{ printf "%s/docker.io/hashicorp/vault-csi-provider" $hostname | quote }}
{{- else if eq $name "secrets-store-csi-provider-openbao" -}}
openbao:
  csi:
    image:
      registry: {{ printf "%s/quay.io" $hostname | quote }}
      repository: openbao/openbao-csi-provider
{{- else if eq $name "fluxcd" -}}
flux2:
  cli:
    image: {{ printf "%s/ghcr.io/fluxcd/flux-cli" $hostname | quote }}
  helmController:
    image: {{ printf "%s/ghcr.io/fluxcd/helm-controller" $hostname | quote }}
  sourceController:
    image: {{ printf "%s/ghcr.io/fluxcd/source-controller" $hostname | quote }}
{{- else if eq $name "snapshot" -}}
snapshot-controller:
  controller:
    image:
      repository: {{ printf "%s/registry.k8s.io/sig-storage/snapshot-controller" $hostname | quote }}
{{- else if eq $name "traefik" -}}
traefik:
  image:
    registry: {{ printf "%s/docker.io" $hostname | quote }}
    repository: traefik
platform:
  traefik:
    ingress:
      bootstrapTLS:
        image:
          repository: {{ printf "%s/docker.io/library/golang" $hostname | quote }}
{{- else if eq $name "trust-manager" -}}
trust-manager:
  image:
    repository: {{ printf "%s/quay.io/jetstack/trust-manager" $hostname | quote }}
  defaultPackageImage:
    repository: {{ printf "%s/quay.io/jetstack/trust-pkg-debian-bookworm" $hostname | quote }}
{{- end -}}
{{- end -}}

{{- define "podplane.platform.baseValues" -}}
{{- $name := .name -}}
{{- if eq $name "podplane-operator" -}}
podplane:
  operator:
    config:
      clusterID: {{ required "platform.components.clusterID is required when podplane-operator is enabled" .clusterID | quote }}
{{ toYaml (.config | default dict) | indent 6 }}
{{- end -}}
{{- end -}}

{{- define "podplane.platform.values" -}}
{{- $name := .name -}}
{{- $values := .values -}}
{{- $userValues := index $values $name | default dict -}}
{{- $baseValues := dict -}}
{{- $renderedBase := include "podplane.platform.baseValues" (dict "name" $name "clusterID" .clusterID "config" .config) -}}
{{- if $renderedBase -}}
{{- $baseValues = fromYaml $renderedBase | default dict -}}
{{- end -}}
{{- $mirrorValues := dict -}}
{{- if and .mirrorEnabled .mirrorHostname -}}
{{- $rendered := include "podplane.platform.mirrorValues" (dict "name" $name "hostname" .mirrorHostname) -}}
{{- if $rendered -}}
{{- $mirrorValues = fromYaml $rendered | default dict -}}
{{- end -}}
{{- end -}}
{{- $merged := mergeOverwrite (deepCopy $baseValues) $mirrorValues $userValues -}}
{{- if $merged }}
  values:
{{ toYaml $merged | indent 4 }}
{{- end -}}
{{- end -}}
