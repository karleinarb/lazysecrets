# Changelog

Every release of lazysecrets, newest first.

## v0.4.1 – 2026-10-27

### Fixed

- An opened link ends on the secret's details, also when its lists were loaded ([#93](https://git.kebcloud.no/keb/lazysecrets/pulls/93)) ([831031b](https://git.kebcloud.no/keb/lazysecrets/commit/831031bb4863805abc7e272f40d5a1d576364a77))

## v0.4.0 – 2026-10-19

### Added

- .NET appsettings files in repos ([#88](https://git.kebcloud.no/keb/lazysecrets/pulls/88)) ([a7db795](https://git.kebcloud.no/keb/lazysecrets/commit/a7db795cbec9425c743153a618dfe04741ac273e))
- A running lazysecrets says when a newer one was installed over it ([#90](https://git.kebcloud.no/keb/lazysecrets/pulls/90)) ([6a7f53e](https://git.kebcloud.no/keb/lazysecrets/commit/6a7f53e2364695e71db8107a21feffe2ddf1a7b9))

### Fixed

- --dry-run in release script ([59c1222](https://git.kebcloud.no/keb/lazysecrets/commit/59c1222f4df04613fb6e945100151568550e70c0))

## v0.3.0 – 2026-10-15

### Added

- Spring Boot config files in repos ([#79](https://git.kebcloud.no/keb/lazysecrets/pulls/79)) ([96395cb](https://git.kebcloud.no/keb/lazysecrets/commit/96395cb6a27a94293e35401790296a894821d075))
- Decrypt Jasypt values in .env and Spring files ([#80](https://git.kebcloud.no/keb/lazysecrets/pulls/80)) ([17da9ea](https://git.kebcloud.no/keb/lazysecrets/commit/17da9eaa3271dfa315a517ad0092ab5cda0b0597))
- H shows what's hidden; Spring keys that don't look secret are hidden ([#81](https://git.kebcloud.no/keb/lazysecrets/pulls/81)) ([913b3db](https://git.kebcloud.no/keb/lazysecrets/commit/913b3db6a938d21c6d8c9285a3111418495c7345))
- Secret patterns for all of a repo's config files, with per-type overrides ([#82](https://git.kebcloud.no/keb/lazysecrets/pulls/82)) ([4bac06a](https://git.kebcloud.no/keb/lazysecrets/commit/4bac06ae5ba6fcb8c96d46198f01235391e29f1a))
- Example env files are listed hidden, not skipped ([#83](https://git.kebcloud.no/keb/lazysecrets/pulls/83)) ([580c7ef](https://git.kebcloud.no/keb/lazysecrets/commit/580c7ef985599b98277d80be38e39abe169b2dc4))
- Docker Compose files in repos ([#84](https://git.kebcloud.no/keb/lazysecrets/pulls/84)) ([0f5248d](https://git.kebcloud.no/keb/lazysecrets/commit/0f5248d8f6d27e3f95f8b9cbc28310ee7b0581f7))
- Go to shows a repo file's icon before its name ([#85](https://git.kebcloud.no/keb/lazysecrets/pulls/85)) ([e2f289d](https://git.kebcloud.no/keb/lazysecrets/commit/e2f289da5c54dab869e7ba10ca35bdc1ab74bf28))

### Fixed

- Decrypt SOPS .env keys written with export or spaces ([#78](https://git.kebcloud.no/keb/lazysecrets/pulls/78)) ([39cbac9](https://git.kebcloud.no/keb/lazysecrets/commit/39cbac9aabcea11e9915363e4adae5873f9724de))

## v0.2.1 – 2026-10-15

### Fixed

- A link opens lazysecrets with your cache and next to your running instances ([#71](https://git.kebcloud.no/keb/lazysecrets/pulls/71)) ([bb41767](https://git.kebcloud.no/keb/lazysecrets/commit/bb41767cbad00bab4f1824f4e0754cc7abd98c59))

## v0.2.0 – 2026-10-14

### Added

- Lazysecrets list prints everything lazysecrets can find, for fzf and editor pickers ([#70](https://git.kebcloud.no/keb/lazysecrets/pulls/70)) ([39454d4](https://git.kebcloud.no/keb/lazysecrets/commit/39454d47a864c8628c50b023aee9539d33be2f44))

### Fixed

- Lazysecrets starts from fzf's become, on a non-blocking terminal ([#67](https://git.kebcloud.no/keb/lazysecrets/pulls/67)) ([4480e8f](https://git.kebcloud.no/keb/lazysecrets/commit/4480e8fed00024691091eb7096abb9dc83b8044d))
- Local builds are dev-<commit> again, not a Go pseudo-version ([#68](https://git.kebcloud.no/keb/lazysecrets/pulls/68)) ([e825a51](https://git.kebcloud.no/keb/lazysecrets/commit/e825a51b30261b55c536af18fefde79dc6308377))
- A wrapped value in the details keeps its colour on every line ([#69](https://git.kebcloud.no/keb/lazysecrets/pulls/69)) ([dbb0eb2](https://git.kebcloud.no/keb/lazysecrets/commit/dbb0eb2bbdf6d695cf0d9ac15a1b513af8ba431d))

## v0.1.2 – 2026-10-13

### Added

- Look for repos in the usual folders, not just ~/git ([#57](https://git.kebcloud.no/keb/lazysecrets/pulls/57)) ([47ef862](https://git.kebcloud.no/keb/lazysecrets/commit/47ef86293d757f965069b1ea100a4d53d5a9b876))
- Config grouped by area, with per-provider timeouts and an update interval ([#60](https://git.kebcloud.no/keb/lazysecrets/pulls/60)) ([10d1fc6](https://git.kebcloud.no/keb/lazysecrets/commit/10d1fc63c75fbb91088119a2e81884e44c36c4a3))
- The update changelog covers every version you missed ([#61](https://git.kebcloud.no/keb/lazysecrets/pulls/61)) ([75bab25](https://git.kebcloud.no/keb/lazysecrets/commit/75bab2528677086219817ff9bc08a36755c96dcd))

### Fixed

- Help and errors name sharing.key and sharing.keyRules, not share.* ([#64](https://git.kebcloud.no/keb/lazysecrets/pulls/64)) ([6b3edd4](https://git.kebcloud.no/keb/lazysecrets/commit/6b3edd45d83b9b2af06a40f1f7e67f35c6861a69))
- Unambiguous repo key names, clearer config and key names, and a newer lazysecrets's files no longer stop an older one ([#66](https://git.kebcloud.no/keb/lazysecrets/pulls/66)) ([a81fcaa](https://git.kebcloud.no/keb/lazysecrets/commit/a81fcaa5a6cef3ce0d07d6195f3e7bbb98808253))

## v0.1.1 – 2026-10-13

### Added

- `e` on the status pane edits the config ([#55](https://git.kebcloud.no/keb/lazysecrets/pulls/55)) ([2044341](https://git.kebcloud.no/keb/lazysecrets/commit/2044341b20d795fa7cadcabb9514631990993e0a))
- Share env files as encrypted links ([#54](https://git.kebcloud.no/keb/lazysecrets/pulls/54)) ([4377eb9](https://git.kebcloud.no/keb/lazysecrets/commit/4377eb98bf0dd523d5c4371bf84668412053290e))

## v0.1.0 – 2026-10-12

Hello world 👋

<!-- generated by git-cliff (cliff.toml) -->
