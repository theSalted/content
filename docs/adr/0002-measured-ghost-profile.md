# ADR 0002: Measured Ghost compatibility profiles

Date: 2026-10-25. Status: proposed.

## Context

The user wants Ghost API parity to retain Ulysses and iA Writer. Public guides describe different authentication principals and workflows but omit exact handshakes. Ghost's complete API includes product features far beyond headless authoring.

## Decision

Pin Ghost 6.72.0 as the reference, with a v6.0 header-based core profile. First prove named client/build/platform workflows. Freeze exact request/response fixtures before claiming a wider profile. Implement an older-path/version compatibility adapter only when traces justify it. All claims disclose implemented endpoints, query operators, content formats and intentional security differences.

## Consequences

The project can deliver useful client support without recreating newsletters/billing/themes. A simple CRUD demo cannot be called parity. Real-app evidence and error/concurrency/format tests become release gates. Supported clients may still offer narrower features than the server; iA's repeated new-draft workflow is not two-way synchronization.

See [matrix](../ghost-compatibility.md) and [test plan](../contract-testing.md).
