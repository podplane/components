{{/*
Podplane <https://podplane.dev>
Copyright The Podplane Authors
SPDX-License-Identifier: Apache-2.0
*/}}
{{- define "csi-gce-pd.identityEnv" -}}
- name: GOOGLE_APPLICATION_CREDENTIALS
  value: /var/run/google/identity/credentials.json
{{- end -}}

{{- define "csi-gce-pd.identityMounts" -}}
- name: google-identity
  mountPath: /var/run/google/identity
  readOnly: true
- name: cloud-config
  mountPath: /etc/gce
  readOnly: true
{{- end -}}

{{- define "csi-gce-pd.identityVolumes" -}}
- name: cloud-config
  configMap:
    name: gce-cloud-config
- name: google-identity
  projected:
    sources:
      - serviceAccountToken:
          audience: {{ printf "https://iam.googleapis.com/%s" .Values.platform.csiGcePd.googleWorkloadIdentity.provider | quote }}
          expirationSeconds: 3600
          path: token
      - configMap:
          name: google-workload-identity
{{- end -}}
