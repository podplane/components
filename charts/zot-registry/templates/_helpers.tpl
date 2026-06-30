{{/*
Podplane <https://podplane.dev>
Copyright The Podplane Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{- define "podplane.zotRegistry.config" -}}
{{- $registry := .Values.platform.zotRegistry | default dict -}}
{{- $storage := $registry.storage | default dict -}}
{{- $oidc := $registry.oidc | default dict -}}

{{- $secure := true -}}
{{- if hasKey $storage "secure" -}}{{- $secure = $storage.secure -}}{{- end -}}

{{- $config := dict
  "distSpecVersion" "1.1.1"
  "storage" (dict
    "rootDirectory" "/var/lib/registry"
    "dedupe" false
    "storageDriver" (dict
      "name" "s3"
      "bucket" ($storage.bucket | default "registry")
      "region" ($storage.region | default "local")
      "forcepathstyle" ($storage.forcePathStyle | default false)
      "secure" $secure
      "skipverify" ($storage.skipVerify | default false)
    )
  )
  "http" (dict
    "address" "0.0.0.0"
    "port" "5000"
    "readTimeout" "60s"
    "writeTimeout" "60s"
    "accessControl" ($registry.accessControl | default dict)
  )
  "log" (dict "level" ($registry.logLevel | default "info"))
-}}

{{/* Zot's S3 driver accepts provider-specific settings as raw JSON keys. */}}
{{- $storageDriver := index (index $config "storage") "storageDriver" -}}
{{- with $storage.endpoint }}{{- $_ := set $storageDriver "regionendpoint" . -}}{{- end -}}
{{- with $storage.rootDirectory }}{{- $_ := set $storageDriver "rootdirectory" . -}}{{- end -}}
{{- with $storage.accessKeyID }}{{- $_ := set $storageDriver "accesskey" . -}}{{- end -}}
{{- with $storage.secretAccessKey }}{{- $_ := set $storageDriver "secretkey" . -}}{{- end -}}

{{/* Direct OIDC bearer-token validation, enabled once issuer/audience are known. */}}
{{- $issuer := $oidc.issuer | default "" -}}
{{- $audience := $oidc.audience | default $registry.registryHostname | default "" -}}
{{- if and $issuer $audience -}}
{{- $http := index $config "http" -}}
{{- $_ := set $http "auth" (dict "bearer" (dict
  "realm" "zot"
  "service" ($registry.registryHostname | default "zot-registry")
  "oidc" (list (dict
    "issuer" $issuer
    "audiences" (list $audience)
    "claimMapping" (dict
      "username" (printf "claims.%s" ($oidc.usernameClaim | default "email"))
      "groups" (printf "claims.%s" ($oidc.groupsClaim | default "groups"))
    )
  ))
)) -}}
{{- end -}}

{{- $config | toPrettyJson -}}
{{- end -}}
