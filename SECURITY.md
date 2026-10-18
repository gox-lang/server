# Security Policy

The security of GOX is a priority. If you discover a potential security vulnerability in GOX, report it through **GitHub Private Vulnerability Reporting**, available in the **Security** section of this repository, so it can be investigated before being made public.

## Reporting a Vulnerability

Potential security vulnerabilities should be reported using **GitHub Private Vulnerability Reporting** in the **Security** section of the repository.

Please **do not publish vulnerabilities or potential vulnerabilities as public GitHub Issues**.

When possible, include:

- A clear description of the issue.
- The steps required to reproduce it.
- The affected GOX versions.
- The security impact you believe it may have.
- Any examples, proof of concept, or additional information that may help reproduce and investigate the issue.

You do not need to determine the severity of the vulnerability beforehand. GOX will evaluate the report and determine its impact and severity during the investigation.

If you are unsure whether an issue has security implications, **report it through GitHub Private Vulnerability Reporting in the Security section of the repository**.

## What Should Be Reported Through GitHub Private Vulnerability Reporting

Use GitHub Private Vulnerability Reporting for issues that may have security implications, including but not limited to:

- Unauthorized access or actions.
- Authentication or authorization issues.
- Exposure of sensitive information.
- Session-related manipulation or vulnerabilities.
- Unintended code execution.
- Vulnerabilities related to paths or filesystem access.
- Security issues related to WebSockets or other communications.
- Vulnerabilities that could cause a denial of service through abnormal or exploitable GOX behavior.
- Any other behavior that could compromise the security of a GOX application, server, or user.

## What Should Not Be Reported as a Vulnerability

Issues that do not have security implications should be reported through **GitHub Issues**.

This includes compilation errors, incorrect API behavior, CLI issues, hot reload issues, tooling problems, framework compatibility issues, performance issues, documentation problems, and other functional bugs that do not represent a security risk.

## Supported Versions

Security vulnerabilities will be evaluated for versions of GOX that are currently supported.

Versions that have reached the end of their support period may not receive security fixes.

## Disclosure

Please do not publicly disclose the details of a vulnerability while it is being investigated and, when applicable, fixed.

Once the issue has been resolved, GOX may publish information about the vulnerability, the affected versions, and the corresponding fix.
