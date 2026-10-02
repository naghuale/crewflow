#!/bin/sh
# Cut a version of crewflow: the fragments of the tasks stand in changelog.d/, the
# unreleased part of the journal gets a number and a date, the fragments go away with
# that change, the tag goes on the commit and the build carries the version instead
# of `dev` (CHANGELOG.md, the rule of [PROJECT_RULES.md](../PROJECT_RULES.md)).
#
#   scripts/release.sh v0.1.0
#
# The journal is not written by a person here, it is built: `crewflow changelog
# release` reads every fragment, gives the section the number and the day of the
# release, leaves an empty unreleased section above and removes the fragments, and
# this script commits that as one release commit. A version with no fragment waiting
# is refused — a version with nothing in it is a version nobody can read. Nothing is
# pushed either — the two commands that push the branch and the tag are printed at
# the end, because a tag is the owner's decision and this script cannot take it back.
set -eu

module=github.com/naghuale/crewflow/internal/buildinfo

die() {
	echo "release: $1" >&2
	echo "usage: scripts/release.sh vMAJOR.MINOR.PATCH" >&2
	exit 1
}

[ $# -eq 1 ] || die "one version is asked for, and none is given"
version=$1

# SemVer as the issue named it: vMAJOR.MINOR.PATCH. No pre-release and no build
# metadata, since a tag of this project is a release and not a candidate.
echo "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' ||
	die "$version is not vMAJOR.MINOR.PATCH"

[ -f CHANGELOG.md ] && [ -f go.mod ] ||
	die "run it in the root of the repository, where CHANGELOG.md and go.mod are"

# The commit that is released is the head of the default branch, so the tree is
# clean and the branch is that one: a tag on a work in progress is not a release.
default_branch=$(sed -n 's/^default_branch *= *"\(.*\)"/\1/p' crewflow.toml | head -n 1)
default_branch=${default_branch:-main}
[ -z "$(git status --porcelain)" ] || die "the working tree is not clean"
branch=$(git symbolic-ref --quiet --short HEAD) ||
	die "HEAD is detached; check out $default_branch first"
[ "$branch" = "$default_branch" ] ||
	die "the branch is $branch, and a version is cut on $default_branch"

if git rev-parse -q --verify "refs/tags/$version" >/dev/null 2>&1; then
	die "the tag $version is already on $(git rev-list -n 1 "$version")"
fi

# There is something to release: a version whose section would hold no line is a
# version a reader has nothing to read, and it is not worth a tag. The folder of
# the fragments need not be there: a project that has released everything has
# none of it, and that is the refusal below and not a wrong folder.

fragments=0
if [ -d changelog.d ]; then
	fragments=$(find changelog.d -maxdepth 1 -name '*.md' -type f | wc -l)
fi
[ "$fragments" -gt 0 ] ||
	die "changelog.d holds no fragment; there is nothing to release"

# The fragments go into the section of the version with the day of the release, the
# empty unreleased part stands above, and the fragments are removed. A fragment that
# is not a fragment, two fragments that say one thing and a line a version below
# already holds are refused here, before any of it reaches the journal of a version,
# and the host is not asked anything: a release does not need it and a version is not
# held up by a task nobody can find.
go run ./cmd/crewflow changelog release "$version"

git add -A
git commit -q -m "chore(release): crewflow $version"
echo "release: committed the journal of $version — $(git rev-parse --short HEAD)"

# The commit that is released is now the one that holds the journal of the version, so
# the version, the commit and the moment go into the binary at link time: a build from
# a tag says which one it is, and a build from a worktree still says `dev`.
commit=$(git rev-parse HEAD)
date=$(date -u +%Y-%m-%dT%H:%M:%SZ)

mkdir -p dist
go build -trimpath \
	-ldflags "-X $module.Version=$version -X $module.Commit=$commit -X $module.Date=$date" \
	-o dist/crewflow ./cmd/crewflow
echo "release: built dist/crewflow — $(./dist/crewflow version)"

# The tag is annotated, so it names the commit without a lookup, and it goes on the
# head of the branch as it is now — the commit that was just built and checked.
git tag -a "$version" -m "crewflow $version"
echo "release: tagged $version at $commit"

cat <<EOF

push the branch and the tag, then cut the release on the host:

  git push origin $default_branch
  git push origin refs/tags/$version
  gh release create $version --title "crewflow $version"

EOF