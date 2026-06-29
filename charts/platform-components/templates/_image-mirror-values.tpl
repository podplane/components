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
agent-sandbox:
  image:
    repository: {{ printf "%s/registry.k8s.io/agent-sandbox/agent-sandbox-controller" $hostname | quote }}
cert-manager:
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
platform-certs:
  platform:
    certs:
      secretSync:
        image:
          repository: {{ printf "%s/docker.io/library/golang" $hostname | quote }}
cilium:
  cilium:
    image:
      repository: {{ printf "%s/quay.io/cilium/cilium" $hostname | quote }}
    operator:
      image:
        repository: {{ printf "%s/quay.io/cilium/operator" $hostname | quote }}
coredns:
  coredns:
    image:
      repository: {{ printf "%s/docker.io/coredns/coredns" $hostname | quote }}
csi-aws-ebs:
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
podplane-operator:
  podplane:
    operator:
      image:
        repository: {{ printf "%s/ghcr.io/podplane/operator" $hostname | quote }}
secrets-store-csi-driver:
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
secrets-store-csi-provider-aws:
  secrets-store-csi-driver-provider-aws:
    image:
      repository: {{ printf "%s/public.ecr.aws/aws-secrets-manager/secrets-store-csi-driver-provider-aws" $hostname | quote }}
secrets-store-csi-provider-gcp:
  gcp:
    image:
      repository: {{ printf "%s/us-docker.pkg.dev/secretmanager-csi/secrets-store-csi-driver-provider-gcp/plugin" $hostname | quote }}
    initImage:
      repository: {{ printf "%s/docker.io/library/busybox" $hostname | quote }}
secrets-store-csi-provider-vault:
  vault:
    csi:
      image:
        repository: {{ printf "%s/docker.io/hashicorp/vault-csi-provider" $hostname | quote }}
secrets-store-csi-provider-openbao:
  openbao:
    csi:
      image:
        registry: {{ printf "%s/quay.io" $hostname | quote }}
        repository: openbao/openbao-csi-provider
fluxcd:
  flux2:
    cli:
      image: {{ printf "%s/ghcr.io/fluxcd/flux-cli" $hostname | quote }}
    helmController:
      image: {{ printf "%s/ghcr.io/fluxcd/helm-controller" $hostname | quote }}
    sourceController:
      image: {{ printf "%s/ghcr.io/fluxcd/source-controller" $hostname | quote }}
snapshot:
  snapshot-controller:
    controller:
      image:
        repository: {{ printf "%s/registry.k8s.io/sig-storage/snapshot-controller" $hostname | quote }}
traefik:
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
trust-manager:
  trust-manager:
    image:
      repository: {{ printf "%s/quay.io/jetstack/trust-manager" $hostname | quote }}
    defaultPackageImage:
      repository: {{ printf "%s/quay.io/jetstack/trust-pkg-debian-bookworm" $hostname | quote }}
{{- end -}}
