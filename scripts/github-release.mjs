// Copies a release GoReleaser published on Gitea to the GitHub mirror:
// the same notes and the same files (dist/), so lazysecrets can update
// from either (internal/update tries GitHub first). Run by the release
// workflow after GoReleaser; a release already on GitHub for the tag is
// replaced, like GoReleaser's mode: replace on Gitea.
//
//   TAG=v0.2.0 GH_TOKEN=… GITEA_TOKEN=… node scripts/github-release.mjs
//
// GH_TOKEN is a fine-grained token that can write the mirror's contents;
// without it, this does nothing. No dependencies: Node 20's fetch.

import { readdir, readFile } from "node:fs/promises";

const github = "karleinarb/lazysecrets";
const { TAG: tag, GH_TOKEN: token, GITEA_TOKEN: giteaToken, GITHUB_SHA: sha } = process.env;
const gitea = `${process.env.GITHUB_SERVER_URL ?? "https://git.kebcloud.no"}/api/v1/repos/${process.env.GITHUB_REPOSITORY ?? "keb/lazysecrets"}`;

if (!token) {
  console.log("No GH_TOKEN: not copying the release to GitHub.");
  process.exit(0);
}
if (!tag) throw new Error("TAG isn't set");

const gh = (path, init = {}) =>
  fetch(path.startsWith("https://") ? path : `https://api.github.com/repos/${github}${path}`, {
    ...init,
    headers: {
      Accept: "application/vnd.github+json",
      Authorization: `Bearer ${token}`,
      "X-GitHub-Api-Version": "2022-11-28",
      ...init.headers,
    },
  });

async function ok(res, what) {
  if (!res.ok) throw new Error(`${what}: ${res.status} ${await res.text()}`);
  return res;
}

// The notes as GoReleaser wrote them on Gitea.
const rel = await (
  await ok(await fetch(`${gitea}/releases/tags/${tag}`, { headers: giteaToken ? { Authorization: `token ${giteaToken}` } : {} }), "reading the Gitea release")
).json();

// Replace what's there for the tag.
const old = await gh(`/releases/tags/${tag}`);
if (old.ok) {
  const { id } = await old.json();
  await ok(await gh(`/releases/${id}`, { method: "DELETE" }), "deleting the old GitHub release");
  console.log(`Deleted the GitHub release for ${tag}.`);
}

// The mirror may not have the tag's commit yet: it pushes on its own.
// Wait for it a while.
const body = JSON.stringify({ tag_name: tag, target_commitish: sha, name: rel.name || tag, body: rel.body, prerelease: rel.prerelease });
let created;
for (let i = 0; ; i++) {
  const res = await gh("/releases", { method: "POST", body });
  if (res.ok) {
    created = await res.json();
    break;
  }
  if (res.status !== 422 || i === 9) await ok(res, "creating the GitHub release");
  console.log("GitHub doesn't have the commit yet; waiting for the mirror…");
  await new Promise((r) => setTimeout(r, 30_000));
}

const files = (await readdir("dist")).filter((f) => f.endsWith(".tar.gz") || f === "checksums.txt");
if (files.length === 0) throw new Error("nothing to upload in dist/");
for (const f of files) {
  const url = `https://uploads.github.com/repos/${github}/releases/${created.id}/assets?name=${encodeURIComponent(f)}`;
  await ok(await gh(url, { method: "POST", headers: { "Content-Type": "application/octet-stream" }, body: await readFile(`dist/${f}`) }), `uploading ${f}`);
  console.log(`Uploaded ${f}.`);
}
console.log(`Published ${tag} on GitHub: ${created.html_url}`);
