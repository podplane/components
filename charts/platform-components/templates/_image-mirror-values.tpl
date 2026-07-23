{{/*
Podplane <https://podplane.dev>
Copyright The Podplane Authors
SPDX-License-Identifier: Apache-2.0

When you deploy a Podplane cluster, you can enable a registry mirror for all component
container images using this chart's `platform.components.imageMirror` value. This
rewrites all component image defaults without changing user-supplied values.

Since each component chart uses a different Helm values structure for its container
images, this helper keeps those chart-specific paths in one place to ease maintenance.

This helper itself is invoked by the `podplane.platform.values` helper in _helpers.tpl
*/}}

{{- define "podplane.platform.imageMirrorValues" -}}
{{- $hostname := .hostname -}}
{{- $prefix := "mirror" -}}
{{- if hasKey . "prefix" -}}{{- $prefix = trimAll "/" .prefix -}}{{- end -}}
{{- $base := $hostname -}}
{{- if $prefix -}}{{- $base = printf "%s/%s" $hostname $prefix -}}{{- end -}}
agent-sandbox:
  image:
    repository: {{ printf "%s/registry.k8s.io/agent-sandbox/agent-sandbox-controller" $base | quote }}
cert-manager:
  cert-manager:
    image:
      repository: {{ printf "%s/quay.io/jetstack/cert-manager-controller" $base | quote }}
    webhook:
      image:
        repository: {{ printf "%s/quay.io/jetstack/cert-manager-webhook" $base | quote }}
    cainjector:
      image:
        repository: {{ printf "%s/quay.io/jetstack/cert-manager-cainjector" $base | quote }}
  cert-manager-approver-policy:
    image:
      repository: {{ printf "%s/quay.io/jetstack/cert-manager-approver-policy" $base | quote }}
platform-certs:
  platform:
    certs:
      secretSync:
        image:
          repository: {{ printf "%s/registry.k8s.io/pause" $base | quote }}
cilium:
  cilium:
    image:
      repository: {{ printf "%s/quay.io/cilium/cilium" $base | quote }}
    operator:
      image:
        repository: {{ printf "%s/quay.io/cilium/operator" $base | quote }}
coredns:
  coredns:
    image:
      repository: {{ printf "%s/docker.io/coredns/coredns" $base | quote }}
cluster-api:
  image:
    repository: {{ printf "%s/registry.k8s.io/cluster-api/cluster-api-controller" $base | quote }}
csi-aws-ebs:
  aws-ebs-csi-driver:
    image:
      repository: {{ printf "%s/public.ecr.aws/ebs-csi-driver/aws-ebs-csi-driver" $base | quote }}
    sidecars:
      provisioner:
        image:
          repository: {{ printf "%s/public.ecr.aws/csi-components/csi-provisioner" $base | quote }}
      attacher:
        image:
          repository: {{ printf "%s/public.ecr.aws/csi-components/csi-attacher" $base | quote }}
      livenessProbe:
        image:
          repository: {{ printf "%s/public.ecr.aws/csi-components/livenessprobe" $base | quote }}
      resizer:
        image:
          repository: {{ printf "%s/public.ecr.aws/csi-components/csi-resizer" $base | quote }}
      nodeDriverRegistrar:
        image:
          repository: {{ printf "%s/public.ecr.aws/csi-components/csi-node-driver-registrar" $base | quote }}
podplane-operator:
  podplane:
    operator:
      image:
        repository: {{ printf "%s/ghcr.io/podplane/operator" $base | quote }}
secrets-store-csi-driver:
  secrets-store-csi-driver:
    linux:
      image:
        repository: {{ printf "%s/registry.k8s.io/csi-secrets-store/driver" $base | quote }}
      crds:
        image:
          repository: {{ printf "%s/registry.k8s.io/csi-secrets-store/driver-crds" $base | quote }}
      registrarImage:
        repository: {{ printf "%s/registry.k8s.io/sig-storage/csi-node-driver-registrar" $base | quote }}
      livenessProbeImage:
        repository: {{ printf "%s/registry.k8s.io/sig-storage/livenessprobe" $base | quote }}
secrets-store-csi-provider-aws:
  secrets-store-csi-driver-provider-aws:
    image:
      repository: {{ printf "%s/public.ecr.aws/aws-secrets-manager/secrets-store-csi-driver-provider-aws" $base | quote }}
secrets-store-csi-provider-gcp:
  gcp:
    image:
      repository: {{ printf "%s/us-docker.pkg.dev/secretmanager-csi/secrets-store-csi-driver-provider-gcp/plugin" $base | quote }}
    initImage:
      repository: {{ printf "%s/docker.io/library/busybox" $base | quote }}
secrets-store-csi-provider-vault:
  vault:
    csi:
      image:
        repository: {{ printf "%s/docker.io/hashicorp/vault-csi-provider" $base | quote }}
secrets-store-csi-provider-openbao:
  openbao:
    csi:
      image:
        registry: {{ printf "%s/quay.io" $base | quote }}
        repository: openbao/openbao-csi-provider
      agent:
        image:
          registry: {{ printf "%s/quay.io" $base | quote }}
          repository: openbao/openbao
fluxcd:
  flux2:
    cli:
      image: {{ printf "%s/ghcr.io/fluxcd/flux-cli" $base | quote }}
    helmController:
      image: {{ printf "%s/ghcr.io/fluxcd/helm-controller" $base | quote }}
    sourceController:
      image: {{ printf "%s/ghcr.io/fluxcd/source-controller" $base | quote }}
snapshot:
  snapshot-controller:
    controller:
      image:
        repository: {{ printf "%s/registry.k8s.io/sig-storage/snapshot-controller" $base | quote }}
traefik:
  traefik:
    image:
      registry: {{ printf "%s/docker.io" $base | quote }}
      repository: traefik
  platform:
    traefik:
      ingress:
        bootstrapTLS:
          image:
            repository: {{ printf "%s/docker.io/library/golang" $base | quote }}
trust-manager:
  trust-manager:
    image:
      repository: {{ printf "%s/quay.io/jetstack/trust-manager" $base | quote }}
    defaultPackageImage:
      repository: {{ printf "%s/quay.io/jetstack/trust-pkg-debian-bookworm" $base | quote }}
zot-registry:
  zot:
    image:
      repository: {{ printf "%s/ghcr.io/project-zot/zot" $base | quote }}
{{- end -}}
