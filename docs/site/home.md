---
layout: home

hero:
  name: Just Enough Certs
  text: 'Keep your site green'
  tagline: 'A self-hosted SSL/TLS certificate manager. It obtains certificates from many sources, validates domains, deploys to CDNs and servers, and renews before expiry.'
  image:
    src: /jec-hero.svg
    alt: Just Enough Certs
  actions:
    - theme: brand
      text: 'Get started'
      link: /guide/getting-started
    - theme: alt
      text: 'What is JEC?'
      link: /guide/introduction
    - theme: alt
      text: 'GitHub'
      link: https://github.com/XMZO/JustEnoughCerts

features:
  - icon: '<svg aria-hidden="true" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M13 22h5a2 2 0 0 0 2-2V8a2.4 2.4 0 0 0-.706-1.706l-3.588-3.588A2.4 2.4 0 0 0 14 2H6a2 2 0 0 0-2 2v3.3"/><path d="M14 2v5a1 1 0 0 0 1 1h5"/><path d="m7.69 16.479 1.29 4.88a.5.5 0 0 1-.698.591l-1.843-.849a1 1 0 0 0-.879.001l-1.846.85a.5.5 0 0 1-.692-.593l1.29-4.88"/><circle cx="6" cy="14" r="3"/></svg>'
    title: 'Many certificate sources'
    details: 'ACME (Let''s Encrypt, ZeroSSL, Google and more, with EAB), the certificate services of Alibaba Cloud and Tencent Cloud, Namecheap, a local directory, or an upload.'
    link: /usage/sources
    linkText: 'Learn more'
  - icon: '<svg aria-hidden="true" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15.686 15A14.5 14.5 0 0 1 12 22a14.5 14.5 0 0 1 0-20 10 10 0 1 0 9.542 13"/><path d="M2 12h8.5"/><path d="M20 6V4a2 2 0 1 0-4 0v2"/><rect width="8" height="5" x="14" y="6" rx="1"/></svg>'
    title: 'Automatic domain validation'
    details: 'Over 200 DNS providers and CNAME delegation; HTTP validation through a website directory, or answered by JEC itself or at the edge of Alibaba Cloud ESA.'
    link: /usage/validation
    linkText: 'Learn more'
  - icon: '<svg aria-hidden="true" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 13v8"/><path d="M4 14.899A7 7 0 1 1 15.71 8h1.79a4.5 4.5 0 0 1 2.5 8.242"/><path d="m8 17 4-4 4 4"/></svg>'
    title: 'Deploy anywhere'
    details: 'Over twenty targets, including Alibaba Cloud, Tencent Cloud, Huawei Cloud, Cloudflare, AWS, Kubernetes, Synology, BaoTa and SSH; every new version replaces the old certificate.'
    link: /usage/targets
    linkText: 'Learn more'
  - icon: '<svg aria-hidden="true" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 10v4h4"/><path d="m11 14 1.535-1.605a5 5 0 018 1.5"/><path d="M16 2v3"/><path d="m21 18-1.535 1.605a5 5 0 01-8-1.5"/><path d="M21 22v-4h-4"/><path d="M21 8.517V5a2 2 0 00-2-2H5a2 2 0 00-2 2v14a2 2 0 002 2h3.517"/><path d="M3 9h4"/><path d="M8 2v3"/></svg>'
    title: 'Renewal before expiry'
    details: 'Certificates renew within their renewal window, following the CA''s renewal information (ARI); failures are retried with backoff, and the certificate actually served is checked.'
    link: /usage/renewal
    linkText: 'Learn more'
  - icon: '<svg aria-hidden="true" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.268 21a2 2 0 0 0 3.464 0"/><path d="M22 8c0-2.3-.8-4.3-2-6"/><path d="M3.262 15.326A1 1 0 0 0 4 17h16a1 1 0 0 0 .74-1.673C19.41 13.956 18 12.499 18 8A6 6 0 0 0 6 8c0 4.499-1.411 5.956-2.738 7.326"/><path d="M4 2C2.8 3.7 2 5.7 2 8"/></svg>'
    title: 'Notifications'
    details: 'More than ten channels, such as email, Telegram, DingTalk, Feishu, WeCom, Bark, ntfy, webhooks and scripts, each with its own events and language.'
    link: /usage/notifications
    linkText: 'Learn more'
  - icon: '<svg aria-hidden="true" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/><path d="m9 12 2 2 4-4"/></svg>'
    title: 'Secure and auditable'
    details: 'Private keys and secrets are encrypted at rest; two-factor authentication, OIDC single sign-on, API tokens and an audit log. Runs as a single container.'
    link: /operations/security
    linkText: 'Learn more'
---
