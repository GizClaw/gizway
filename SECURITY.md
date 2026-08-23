# Security Policy

## Supported versions

Security updates target the current `main` branch and the latest published
release unless another supported release line is announced explicitly.

## Reporting a vulnerability

Do not report security vulnerabilities through public GitHub issues.

Use GitHub private vulnerability reporting:

https://github.com/GizClaw/gizway/security/advisories/new

Include the affected component and revision, reproduction steps or proof of
concept, expected impact, and any known mitigation. We will acknowledge valid
reports as soon as practical and coordinate remediation and disclosure based on
severity and exploitability.

## Scope

In scope:

- GizPay and GizWay application code and APIs;
- browser SDK code maintained in this repository;
- Entry, PowerSync, and identity integration code owned here;
- repository CI, packaging, signing, and release automation.

Third-party service vulnerabilities are out of scope unless GizWay integration
code directly creates or amplifies the exposure. Reports should not include
production credentials, personal data, or destructive testing against systems
you do not own.
