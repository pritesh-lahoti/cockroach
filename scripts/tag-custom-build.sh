#!/usr/bin/env bash

# Copyright 2020 The Cockroach Authors.
#
# Use of this software is governed by the CockroachDB Software License
# included in the /LICENSE file.


# This script makes it easy to make custom builds.
#
# It creates a tag for a SHA that triggers a build in the Make and Publish
# Build TeamCity build config. Once the build is complete, binaries and a
# docker image are available. For details on how to validate everything is
# correct and how to use the binaries/docker image, see:
# https://cockroachlabs.atlassian.net/wiki/spaces/ENG/pages/846299518/One-Off+Builds+A+How+To
#
# How to use this script:
#
# 1) To tag the checked out SHA (the script is not available for releases
#    v20.1.5, v19.2.10 and older; use option 2 for those releases) run it
#    with no arguments from the root of the repo.
#
# 2) To tag a non-checked out SHA including any SHAs on releases (or branches)
#    older than v20.1.5 and v19.2.10, run it from the root of the repo with
#    the SHA that you want to tag as the single argument.
#
#      ./scripts/tag-custom-build.sh "$SHA"
#
#    Use the --jj flag to get the current SHA from jj instead of git:
#
#      ./scripts/tag-custom-build.sh --jj
#
# Note the Tag Name and Build ID (printed at the end of the script output).
#
# Verify the SHA on the GitHub page for the tag (it should open automatically
# in your browser) is the one you tagged. (If the page didn't open in your
# browser, the tag should be somewhere in this list, not necessarily at the top:
# https://github.com/cockroachdb/cockroach/tags .)
#
# Use the tag name to find the build in the Make and Publish Build build config
# in TeamCity.
#
# Use the Build ID when referencing the binaries and docker image with others.

set -euo pipefail

use_jj=false

# Parse command line options
while getopts ":j-:" opt; do
  case $opt in
    j)
      use_jj=true
      ;;
    -)
      case "${OPTARG}" in
        jj)
          use_jj=true
          ;;
        *)
          echo "Invalid option: --${OPTARG}" >&2
          exit 1
          ;;
      esac
      ;;
    \?)
      echo "Invalid option: -$OPTARG" >&2
      exit 1
      ;;
  esac
done

# Shift past the processed options
shift $((OPTIND-1))

# Get SHA from positional parameter if provided
SHA="${1-}"

if [ -z "$SHA" ] ; then
    if [ "$use_jj" = true ] ; then
        SHA="$(jj log -r@ -n1 --template commit_id --no-graph)"
    else
        SHA="$(git rev-parse HEAD)"
    fi
fi

# Ensure all the latest tags are downloaded locally
git fetch -t

ID="$(git describe --tags --match=v[0-9]* "$SHA")"
TAG="custombuild-$ID"

git push git@github.com:cockroachdb/cockroach.git "$SHA:refs/tags/$TAG"

TAG_URL="https://github.com/cockroachdb/cockroach/releases/tag/${TAG}"
TEAMCITY_URL="https://teamcity.cockroachdb.com/buildConfiguration/Internal_Cockroach_Release_Customized_MakeAndPublishCustomizedBuild?mode=builds&branch=${TAG}"
if [ "$(command -v open)" ] ; then
    open "$TEAMCITY_URL"
    open "$TAG_URL"
elif [ "$(command -v xdg-open)" ] ; then
    xdg-open "$TEAMCITY_URL"
    xdg-open "$TAG_URL"
fi

cat << EOF

See the one-off builds wiki page for steps for the rest of the process:

  https://cockroachlabs.atlassian.net/wiki/spaces/ENG/pages/846299518/One-Off+Builds+A+How+To

Here is the tag in GitHub:

  $TAG_URL

Here is where the build run should show up in TeamCity for the tag:

  $TEAMCITY_URL

Tag name: $TAG
Build ID: $ID

The binaries will be available at:
  https://storage.googleapis.com/cockroach-customized-builds-artifacts-prod/cockroach-$ID.linux-amd64.tgz
  https://storage.googleapis.com/cockroach-customized-builds-artifacts-prod/cockroach-$ID.linux-amd64-fips.tgz
  https://storage.googleapis.com/cockroach-customized-builds-artifacts-prod/cockroach-$ID.linux-arm64.tgz
  https://storage.googleapis.com/cockroach-customized-builds-artifacts-prod/cockroach-$ID.linux-s390x.tgz
  https://storage.googleapis.com/cockroach-customized-builds-artifacts-prod/cockroach-$ID.darwin-11.0-arm64.tgz
  https://storage.googleapis.com/cockroach-customized-builds-artifacts-prod/cockroach-$ID.darwin-10.9-amd64.tgz
  https://storage.googleapis.com/cockroach-customized-builds-artifacts-prod/cockroach-$ID.windows-6.2-amd64.zip

Pull the docker image by:
  docker pull us-docker.pkg.dev/cockroach-cloud-images/cockroachdb-customized/cockroach-customized:$ID
  docker pull us-docker.pkg.dev/cockroach-cloud-images/cockroachdb-customized/cockroach-customized:$ID-fips

EOF
