# Roadmap

This is a self-hosted, API-first headless CMS for private/internal use
across multiple projects — not a commercial SaaS product. No billing,
payments, public registration, marketplace, website hosting, or AI
features. See PROGRESS.md for current status against the phases below.

## Phases (from the original spec)

0. Architecture and foundation — mostly done, see PROGRESS.md.
1. First complete vertical slice (owner login → project → content type →
   entry → publish → read via API) — **done and verified**.
2. Complete content engine: remaining field types, safe schema
   migrations, relations, components, dynamic zones, filtering/sorting/
   pagination, bulk actions — not started beyond what phase 1 needed.
3. Publishing and media: revisions, localization, scheduling, media
   library, storage adapters, image processing — not started.
4. Security and integrations: granular RBAC, API tokens, MFA, webhooks,
   durable jobs, audit history, API explorer, TypeScript SDK — not
   started.
5. Performance: query optimization, response caching, benchmark fixtures,
   load testing, profiling, Strapi comparison — not started.
6. Product craftsmanship: full design-system coverage, accessibility
   audit, visual regression testing — partially benefits from b4 already
   being accessible-by-design, but no dedicated audit has been done.
7. Production readiness: security audit, dependency audit, backup/restore
   drill, long-duration load testing — not started.

## Next up

See PROGRESS.md "Exact next steps" — RBAC and relations are next, in that
order, because almost everything in phase 2+ depends on one or both.
