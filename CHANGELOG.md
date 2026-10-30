# Changelog

## [0.2.0](https://github.com/LoriKarikari/terril/compare/v0.1.0...v0.2.0) (2026-10-30)


### ⚠ BREAKING CHANGES

* rename wolkpack to terril ([#30](https://github.com/LoriKarikari/terril/issues/30))

### Features

* **dotnet:** build ASP.NET Core and console apps ([#35](https://github.com/LoriKarikari/terril/issues/35)) ([8e58741](https://github.com/LoriKarikari/terril/commit/8e58741fed093618bfc0ffd7dbc135027e4aa886))
* rename wolkpack to terril ([#30](https://github.com/LoriKarikari/terril/issues/30)) ([2ebfc50](https://github.com/LoriKarikari/terril/commit/2ebfc502a44d0ce8cdb7e89f618c90a952536b0d))
* **rust:** build Rust binaries with cargo-chef ([#36](https://github.com/LoriKarikari/terril/issues/36)) ([8a98b77](https://github.com/LoriKarikari/terril/commit/8a98b7724b55ac9b65917c0a7592d9b386d68c62))

## 0.1.0 (2026-10-27)


### Features

* **cli:** add wolkpack version, and release with release-please and GoReleaser ([#24](https://github.com/LoriKarikari/wolkpack/issues/24)) ([8b1fb33](https://github.com/LoriKarikari/wolkpack/commit/8b1fb33e330f1182bb13d3c0395baa8a76369825))
* **cli:** pass build-time variables with --env ([#13](https://github.com/LoriKarikari/wolkpack/issues/13)) ([61bf3af](https://github.com/LoriKarikari/wolkpack/commit/61bf3afac39c83c6398bdf2d10490faed614a5dc))
* **config:** read provider, start and apt packages from wolkpack.toml ([#4](https://github.com/LoriKarikari/wolkpack/issues/4)) ([74cbe22](https://github.com/LoriKarikari/wolkpack/commit/74cbe224912feeac7dfdd61e18adc8e4c3f4511d))
* **go:** build apps that use cgo ([#15](https://github.com/LoriKarikari/wolkpack/issues/15)) ([a0b8f67](https://github.com/LoriKarikari/wolkpack/commit/a0b8f67482ed571b77f3e50601d4ab2c905d1653))
* **node:** build one app out of a JS workspace ([#5](https://github.com/LoriKarikari/wolkpack/issues/5)) ([6601b75](https://github.com/LoriKarikari/wolkpack/commit/6601b75e8892b2f7f11d90e9e39e4a1616d1854b))
* **node:** build with Yarn, Bun and Poetry ([#2](https://github.com/LoriKarikari/wolkpack/issues/2)) ([4059754](https://github.com/LoriKarikari/wolkpack/commit/405975423ea8a458191f93fcc92ffa8acae26388))
* **node:** run Next.js and Nuxt from their build output ([#14](https://github.com/LoriKarikari/wolkpack/issues/14)) ([7b877c7](https://github.com/LoriKarikari/wolkpack/commit/7b877c7d0acfa05bb6a037a47a7dcbebcb0470a5))
* **php:** build PHP and Laravel apps on FrankenPHP ([#21](https://github.com/LoriKarikari/wolkpack/issues/21)) ([d239288](https://github.com/LoriKarikari/wolkpack/commit/d239288e6c87d46e745f24b696fd5a75729fa192))
* **python:** build one app out of a uv workspace ([#16](https://github.com/LoriKarikari/wolkpack/issues/16)) ([c8b3643](https://github.com/LoriKarikari/wolkpack/commit/c8b36436cd317fd1f69fde1601d7c64d4fcb987c))
* **python:** start Django and Flask, and run collectstatic ([#1](https://github.com/LoriKarikari/wolkpack/issues/1)) ([e8563bb](https://github.com/LoriKarikari/wolkpack/commit/e8563bb6ebe9377d529cbb3f4b60677cb3cc3a55))
* **ruby:** build Rails and Rack apps ([#20](https://github.com/LoriKarikari/wolkpack/issues/20)) ([4873a05](https://github.com/LoriKarikari/wolkpack/commit/4873a05d46c2f8eef5403b256a10bf58cb967916))
* **static:** serve static sites and Vite apps with Caddy ([#11](https://github.com/LoriKarikari/wolkpack/issues/11)) ([46a44a5](https://github.com/LoriKarikari/wolkpack/commit/46a44a52b85973bf753b3413d2254825079aa300))
* wolkpack, a Dockerfile-based app builder for Node, Python and Go ([ae67e1e](https://github.com/LoriKarikari/wolkpack/commit/ae67e1e1358a35fccfe7c04d30be9c957dc5a676))


### Fixes

* **node:** build and prune Yarn, Bun and plain npm workspaces ([#7](https://github.com/LoriKarikari/wolkpack/issues/7)) ([d5e339d](https://github.com/LoriKarikari/wolkpack/commit/d5e339d0d77db5b4540c868554b482748ea2cbd2))
* **node:** prune workspace builds to the app's production dependencies ([#6](https://github.com/LoriKarikari/wolkpack/issues/6)) ([af9a524](https://github.com/LoriKarikari/wolkpack/commit/af9a5246693e6e110226fbbb1015d650ef4890a4))
* **php:** pin FrankenPHP 1.13.3's frozen binaries ([#25](https://github.com/LoriKarikari/wolkpack/issues/25)) ([84e0796](https://github.com/LoriKarikari/wolkpack/commit/84e079603b49ccbf8e7e18ece4145d9d53221a10))


### Performance

* keep workspace dependency installs cached, and gate on it in CI ([#19](https://github.com/LoriKarikari/wolkpack/issues/19)) ([8b57de8](https://github.com/LoriKarikari/wolkpack/commit/8b57de86d23b6c94cfaafb6937f9f5e9fb4fc98a))
* **node:** run plain node start commands on distroless ([#3](https://github.com/LoriKarikari/wolkpack/issues/3)) ([db383e5](https://github.com/LoriKarikari/wolkpack/commit/db383e598abfb5d3fac02cbdaadb9cbf2ad84ac1))
* run Python, Bun, Node bin starts and static sites on distroless ([#18](https://github.com/LoriKarikari/wolkpack/issues/18)) ([a25a6d5](https://github.com/LoriKarikari/wolkpack/commit/a25a6d5dd53e63ad1dd10f47828c9162ed0992f4))
