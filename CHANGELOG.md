# Changelog

## [0.1.14](https://github.com/meigma/release/compare/v0.1.13...v0.1.14) (2026-08-22)


### Bug Fixes

* **packages:** trust public APT TLS ([#56](https://github.com/meigma/release/issues/56)) ([dea9929](https://github.com/meigma/release/commit/dea992948c68107e2e27d445fb627a62cd99485c))

## [0.1.13](https://github.com/meigma/release/compare/v0.1.12...v0.1.13) (2026-08-22)


### Bug Fixes

* **packages:** verify exact APK version ([#54](https://github.com/meigma/release/issues/54)) ([66ce9a5](https://github.com/meigma/release/commit/66ce9a5ee8b6a7a2131dbb04162f45fa7c6a18fe))

## [0.1.12](https://github.com/meigma/release/compare/v0.1.11...v0.1.12) (2026-08-22)


### Bug Fixes

* **packages:** match APK index key name ([#52](https://github.com/meigma/release/issues/52)) ([bc7531d](https://github.com/meigma/release/commit/bc7531d14dac8acf06e1e1a71b9a12e795be1313))

## [0.1.11](https://github.com/meigma/release/compare/v0.1.10...v0.1.11) (2026-08-22)


### Bug Fixes

* **packages:** expose public output to clients ([#50](https://github.com/meigma/release/issues/50)) ([abd33a0](https://github.com/meigma/release/commit/abd33a0c75a51882a24f9a4b1a6ce1a24f8265a9))

## [0.1.10](https://github.com/meigma/release/compare/v0.1.9...v0.1.10) (2026-08-22)


### Bug Fixes

* **packages:** restore generated tree ownership ([#48](https://github.com/meigma/release/issues/48)) ([c704d1c](https://github.com/meigma/release/commit/c704d1cc599b3fceb69c275822ba0b55d0613edc))

## [0.1.9](https://github.com/meigma/release/compare/v0.1.8...v0.1.9) (2026-08-22)


### Bug Fixes

* **packages:** honor APK signature key name ([#46](https://github.com/meigma/release/issues/46)) ([9870697](https://github.com/meigma/release/commit/987069787e983f1627146fa23b7bd96306ce8ed8))

## [0.1.8](https://github.com/meigma/release/compare/v0.1.7...v0.1.8) (2026-08-22)


### Features

* **release:** sign native packages ([#44](https://github.com/meigma/release/issues/44)) ([55732d5](https://github.com/meigma/release/commit/55732d553dac5223c5eaa24c1fcda366b7976689))

## [0.1.7](https://github.com/meigma/release/compare/v0.1.6...v0.1.7) (2026-08-22)


### Features

* **cli:** initialize Scoop buckets ([#41](https://github.com/meigma/release/issues/41)) ([e657e15](https://github.com/meigma/release/commit/e657e1523cc5dd5551ad3b2cbc6b9fb1d4e64cfc))
* **release:** publish native package repositories ([#43](https://github.com/meigma/release/issues/43)) ([8c30447](https://github.com/meigma/release/commit/8c304474a3992d75cd461cbebc5142986b736746))

## [0.1.6](https://github.com/meigma/release/compare/v0.1.5...v0.1.6) (2026-08-21)


### Features

* **ci:** publish Scoop manifests after releases ([#39](https://github.com/meigma/release/issues/39)) ([38fde4f](https://github.com/meigma/release/commit/38fde4f8e33c9270a80a88fd0dc015821c50ccd9))
* **cli:** publish Scoop manifests through pull requests ([#37](https://github.com/meigma/release/issues/37)) ([66f43c7](https://github.com/meigma/release/commit/66f43c7906b12a201b7530fad043ac9a77974076))

## [0.1.5](https://github.com/meigma/release/compare/v0.1.4...v0.1.5) (2026-08-21)


### Bug Fixes

* **release:** create Homebrew cask directory ([#34](https://github.com/meigma/release/issues/34)) ([153110d](https://github.com/meigma/release/commit/153110d085202ff43b505e5f56d5a00c2a51b6ab))

## [0.1.4](https://github.com/meigma/release/compare/v0.1.3...v0.1.4) (2026-08-21)


### Features

* **ci:** add managed Homebrew tap validation ([#28](https://github.com/meigma/release/issues/28)) ([abd25d2](https://github.com/meigma/release/commit/abd25d2f066e544551a502d1f260934955019aeb))
* **cli:** initialize Homebrew taps ([#33](https://github.com/meigma/release/issues/33)) ([cde9a81](https://github.com/meigma/release/commit/cde9a81a3d41486361b1dd2b76f388f7b3d81e71))
* **cli:** publish Homebrew casks through tap PRs ([#31](https://github.com/meigma/release/issues/31)) ([4ae2da5](https://github.com/meigma/release/commit/4ae2da5399b8506f351a2884c44cd82b0e1e3614))
* **release:** publish signed Homebrew casks ([#32](https://github.com/meigma/release/issues/32)) ([49bb930](https://github.com/meigma/release/commit/49bb930f171b488a497cca838ca30b5b4ba9ff96))


### Bug Fixes

* **ci:** scope tap validation to changed casks ([#30](https://github.com/meigma/release/issues/30)) ([c933580](https://github.com/meigma/release/commit/c9335805068db1bc64c33d9af66cbb8d58657864))

## [0.1.3](https://github.com/meigma/release/compare/v0.1.2...v0.1.3) (2026-08-20)


### Features

* **nix:** add release-cli flake ([#25](https://github.com/meigma/release/issues/25)) ([df48f02](https://github.com/meigma/release/commit/df48f02f00c38886ca6cde307e18fa301db7052b))

## [0.1.2](https://github.com/meigma/release/compare/v0.1.1...v0.1.2) (2026-08-20)


### Features

* **release:** attach native Linux packages ([#21](https://github.com/meigma/release/issues/21)) ([956fb30](https://github.com/meigma/release/commit/956fb30c73915c31abd11227f893ee9d4ef360c7))

## [0.1.1](https://github.com/meigma/release/compare/v0.1.0...v0.1.1) (2026-08-20)


### Features

* **image:** build OCI layouts from staged binaries ([#15](https://github.com/meigma/release/issues/15)) ([e235a28](https://github.com/meigma/release/commit/e235a28ad0643f6bf00d59c9323f9176aaa81aae))
* **image:** verify OCI image contracts ([#16](https://github.com/meigma/release/issues/16)) ([8a5e0a7](https://github.com/meigma/release/commit/8a5e0a721cc6e3ec681e6d3d5461092da993a7f1))
* **oci:** finalize trusted image tags ([#12](https://github.com/meigma/release/issues/12)) ([3a649f0](https://github.com/meigma/release/commit/3a649f041c94bda4e550bac4a810d5ae84a8fdcb))
* **oci:** plan immutable release tags ([#10](https://github.com/meigma/release/issues/10)) ([de75a92](https://github.com/meigma/release/commit/de75a92b2f8651d419971a6b06a2642a6119e8db))
* **oci:** prepare digest publication ([#11](https://github.com/meigma/release/issues/11)) ([257ac5f](https://github.com/meigma/release/commit/257ac5f830b04b473099bf6f3c6a24656c31a526))
* **release:** publish verified GitHub releases ([#14](https://github.com/meigma/release/issues/14)) ([df077f9](https://github.com/meigma/release/commit/df077f9e7688232e7f0d070c232fdb12bc64c0e5))
* **release:** verify signed release bundles ([#13](https://github.com/meigma/release/issues/13)) ([6930882](https://github.com/meigma/release/commit/693088286c854e27d800d19ec4175f3a3aacd4aa))

## 0.1.0 (2026-08-19)


### Features

* **cli:** stage Go release artifacts ([#5](https://github.com/meigma/release/issues/5)) ([da64bb3](https://github.com/meigma/release/commit/da64bb3cfb041be6dacb6a8f7bd83b0c34f89612))
* **cli:** verify Actions artifact handoffs ([#7](https://github.com/meigma/release/issues/7)) ([35ed2f9](https://github.com/meigma/release/commit/35ed2f9f6810c0d00d5b11bff3790969c261f247))
* **release:** establish Go delivery MVP ([#2](https://github.com/meigma/release/issues/2)) ([5566640](https://github.com/meigma/release/commit/5566640c061c5e36f3715e0a1b57eaf69646a0ba))
