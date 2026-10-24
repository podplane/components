# CoreDNS Helm Chart

## Static DNS records

Set `platform.coredns.staticRecords` to map hostnames to IPv4/IPv6 address lists:

```yaml
platform:
  coredns:
    staticRecords:
      host.podplane.internal: ["10.0.2.2"]
```

Through `platform-components`, nest these values under `platform.components.values.coredns`.
The map defaults to empty and supports A/AAAA and automatic PTR records.

Podplane seeds the host alias for new local clusters; the local components
bootstrap targets configure it too. Restarting an existing cluster does not add
it. Cloud clusters have no default records.
