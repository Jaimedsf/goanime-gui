// Package animefire talks to the AnimeFire source (PT-BR catalog): search,
// episode listing, and stream resolution via the site's JSON API at
// api.animefire.one (it moved from animefire.io in 2026-09). The site was rebuilt as a single-page app — the old
// server-rendered /pesquisar and /animes/<slug> routes now 404 — so nothing
// here scrapes HTML any more. Leaf provider package — depends only on
// netx/util/models.
package animefire
