{{/*
Podplane <https://podplane.dev>
Copyright The Podplane Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{- define "podplane.zotRegistry.config" -}}
{{- $registry := .Values.platform.zotRegistry | default dict -}}
{{- $storage := $registry.storage | default dict -}}
{{- $oidc := $registry.oidc | default dict -}}

{{- $driver := $storage.driver | default "s3" -}}
{{- if and $storage.googleSecretVersion (or (ne $driver "gcs") (not $storage.keyfile)) -}}
{{- fail "platform.zotRegistry.storage.googleSecretVersion requires gcs storage and a configured keyfile" -}}
{{- end -}}
{{- $storageDriver := dict "name" $driver "bucket" ($storage.bucket | default "registry") -}}
{{- if eq $driver "s3" -}}
{{- $secure := true -}}
{{- if hasKey $storage "secure" -}}{{- $secure = $storage.secure -}}{{- end -}}
{{- $region := $storage.region | default "local" -}}
{{- if and (eq $region "local") (not $storage.endpoint) -}}
{{- fail "platform.zotRegistry.storage.endpoint is required when platform.zotRegistry.storage.region is local" -}}
{{- end -}}
{{- $_ := set $storageDriver "region" $region -}}
{{- $_ := set $storageDriver "forcepathstyle" ($storage.forcePathStyle | default false) -}}
{{- $_ := set $storageDriver "secure" $secure -}}
{{- $_ := set $storageDriver "skipverify" ($storage.skipVerify | default false) -}}
{{- /* Zot's S3 driver accepts provider-specific settings as raw JSON keys. */ -}}
{{- with $storage.endpoint }}{{- $_ := set $storageDriver "regionendpoint" . -}}{{- end -}}
{{- with $storage.rootDirectory }}{{- $_ := set $storageDriver "rootdirectory" . -}}{{- end -}}
{{- with $storage.accessKeyID }}{{- $_ := set $storageDriver "accesskey" . -}}{{- end -}}
{{- with $storage.secretAccessKey }}{{- $_ := set $storageDriver "secretkey" . -}}{{- end -}}
{{- else if eq $driver "gcs" -}}
{{- /*
Zot replaces an empty or "/" GCS rootdirectory with "/zot", but the node-local
Podplane registry reads repositories from the bucket root. The upstream GCS
driver trims slashes from rootdirectory, so "//" keeps keys at the bucket root.
*/ -}}
{{- $_ := set $storageDriver "rootdirectory" ($storage.rootDirectory | default "//") -}}
{{- with $storage.keyfile }}{{- $_ := set $storageDriver "keyfile" . -}}{{- end -}}
{{- else -}}
{{- fail (printf "platform.zotRegistry.storage.driver must be s3 or gcs, got %q" $driver) -}}
{{- end -}}
{{- $issuer := required "platform.zotRegistry.oidc.issuer is required" $oidc.issuer -}}
{{- $audience := required "platform.zotRegistry.oidc.audience is required" $oidc.audience -}}

{{- $config := dict
  "distSpecVersion" "1.1.1"
  "storage" (dict
    "rootDirectory" "/var/lib/registry"
    "dedupe" false
    "storageDriver" $storageDriver
  )
  "http" (dict
    "address" "0.0.0.0"
    "port" "5000"
    "compat" (list "docker2s2")
    "readTimeout" "60s"
    "writeTimeout" "60s"
    "accessControl" ($registry.accessControl | default dict)
    "auth" (dict "bearer" (dict
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
    ))
  )
  "log" (dict "level" ($registry.logLevel | default "info"))
-}}
{{- with $oidc.certificateAuthority }}{{- $_ := set (index (index (index (index $config "http") "auth") "bearer") "oidc" | first) "certificateAuthority" . -}}{{- end -}}
{{- with $oidc.certificateAuthorityFile }}{{- $_ := set (index (index (index (index $config "http") "auth") "bearer") "oidc" | first) "certificateAuthorityFile" . -}}{{- end -}}

{{- $config | toPrettyJson -}}
{{- end -}}
