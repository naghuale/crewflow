#!/bin/sh
# Cut a version of crewflow: the section of the journal gets a number and a date,
# the tag goes on the commit, and the build carries that version instead of `dev`
# (CHANGELOG.md, the rule of [PROJECT_RULES.md](../PROJECT_RULES.md)).
#
#   scripts/release.sh v0.1.0
#
# The journal is written by a person, so this script never writes it: it refuses to
# do anything until the section for the version is there and the unreleased one is
# empty, and it says which of the two is missing. Nothing is pushed either — the two
# commands that push the branch and the tag are printed at the end, because a tag is
# the owner's decision and this script cannot take it back.
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

# The section of the release is in the journal with its date, and the unreleased one
# above it is empty: a version that takes lines away from the next release is a
# version whose history a person cannot follow.
grep -Eq "^## \[$version\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" CHANGELOG.md ||
	die "CHANGELOG.md has no section \"## [$version] - YYYY-MM-DD\"; give \"## [Не выпущено]\" that number and the date of the release, and merge that change first"
unreleased=$(awk '/^## \[Не выпущено\]/{inside=1;next} /^## /{inside=0} inside' CHANGELOG.md)
if printf '%s\n' "$unreleased" | grep -q '^- '; then
	die "the section \"## [Не выпущено]\" still holds lines; a release leaves nothing unreleased behind"
fi

if git rev-parse -q --verify "refs/tags/$version" >/dev/null 2>&1; then
	die "the tag $version is already on $(git rev-list -n 1 "$version")"
fi

commit=$(git rev-parse HEAD)
date=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# The version, the commit and the moment go into the binary at link time: a build
# from a tag says which one it is, and a build from a worktree still says `dev`.
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