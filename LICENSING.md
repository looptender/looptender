# Licensing looptender

looptender uses the [Sustainable Use License, Version 1.0](LICENSE), with separate commercial agreements for uses outside that license. Connor Burgess is the licensor and project steward. This guide explains the policy; it does not add restrictions to or replace the license text.

The source is public, but the commercial-use restrictions mean this is **source-available**, not OSI-approved open source. The same rules apply to everyone, including companies that make other agent tools. There is no list of prohibited competitors.

## Everyday use and commercial products

looptender is a supervisor that a person or a company runs for their own agent sessions.

| Use | Under the Sustainable Use License |
| --- | --- |
| Use it personally to supervise your own agent sessions | Permitted without a license fee |
| Use it inside your company to supervise your company's own agents and work | Permitted without a license fee |
| Use your own installation while consulting or doing paid client work | Permitted as your internal business use; this does not include selling customers access to looptender |
| Inspect or modify it for a permitted use | Permitted |
| Share a fork or copy free of charge for noncommercial purposes | Permitted with the license, notices, and a prominent notice of modifications |
| Sell or commercially redistribute looptender or a modified version | Requires a separate commercial agreement |
| Ship looptender or its implementation inside a commercial product, such as an IDE, an agent host or a managed service | Requires a separate commercial agreement |
| Sell access to looptender as a hosted service | Requires a separate commercial agreement |

An agent or tool that makes itself supervisable, or that calls a permitted looptender installation through its command line and JSON output, without incorporating or distributing looptender's implementation, does not by itself require a commercial agreement. The other tool's terms still apply. Using the code and other work your agents produce while looptender supervises them, including in paid work, is not redistribution of looptender's software.

A working test for the area between internal use and a service: use is permitted unless you sell a product, service or module whose commercial value derives entirely or substantially from looptender's functionality. This is how the license's authors read the same text for their own software (see the sources below); it is a guide to reading this license, not an addition to it.

The distinction is how the software is used or distributed, not whether its user earns money. Charging for a product or service that contains or provides looptender is different from running looptender yourself while doing paid work. If a case does not clearly fit the license, request terms before shipping it.

For commercial terms, open a [licensing inquiry](https://github.com/looptender/looptender/issues/new?title=Commercial%20licensing%20inquiry) requesting a private contact. Keep confidential business details out of public issues. A commercial agreement is separate from buying the project; an inquiry does not grant permission.

## Third-party material

Third-party dependencies, assets, and license documents retain their own terms; the project license does not relicense them. The terms of the software inside the executable, the Go runtime and standard library and the modules it links, are reproduced in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES), which ships in every release archive.

## Future versions

The licensor may offer future versions of looptender under different terms, and may offer separate terms to a particular person or company. A version that has already been published keeps the terms it was published under for anyone who received it.

## Contributions

New contributions follow [CONTRIBUTING.md](CONTRIBUTING.md) and the [contributor agreement](CONTRIBUTOR_LICENSE_AGREEMENT.md). Contributors retain copyright and give the steward permission to license their contributions under other terms, including commercial terms. The agreement applies only when accepted; it is not retroactive and does not convert earlier DCO sign-offs into consent.

The original [DCO 1.1](LICENSES/DCO-1.1.txt) is preserved unchanged for historical reference. Current commit sign-offs record contribution origin; they do not certify DCO 1.1 or replace explicit contributor-agreement acceptance.

## Sources of the standard terms

- The Sustainable Use License body is reproduced unchanged from [n8n's license](https://github.com/n8n-io/n8n/blob/01bd57cddf11e8173b16adce1b22a8eb94598672/LICENSE.md#sustainable-use-license). n8n-specific enterprise and branch exclusions are not part of looptender's policy.
- The working test for a product whose value derives from the software is the reading n8n publishes for the same license text in its [license FAQ](https://docs.n8n.io/n8n-community-license/community-license/license-faq), linked from its [explanation of the license](https://docs.n8n.io/n8n-community-license). n8n's examples and its rules about its own product are not part of looptender's policy.
- The short contributor agreement follows the [Indie Open Source Tiny CLA](https://github.com/indieopensource/tiny-cla/blob/v1.0.0/cla.md), also used by [n8n](https://github.com/n8n-io/n8n/blob/01bd57cddf11e8173b16adce1b22a8eb94598672/CONTRIBUTOR_LICENSE_AGREEMENT.md), with Connor Burgess named as steward.
