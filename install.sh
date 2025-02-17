#!/usr/bin/env bash

set -e +o pipefail

show_help() {
cat << EOF

    Cloud Command Line Interface Installer

    -h          Display this help and exit

    -V VERSION  Custom CLI version. Default is 'latest'

EOF
}

abort() {
  printf "%s\n" "$@" >&2
  exit 1
}

# First check OS.
OS="$(uname)"
if [[ "${OS}" == "Linux" ]]
then
  CLI_ON_LINUX=1
elif [[ "${OS}" == "Darwin" ]]
then
  CLI_ON_MACOS=1
else
  abort "Currently is only supported on macOS and Linux."
fi

VERSION="latest"

if [ $# != 0 ];
then
  while getopts "hV:-" o
  do
    case "$o" in
      "h")
        show_help
        exit 0;
        ;;
      "V")
        VERSION="$OPTARG"
        ;;
      *)
        echo -e "Unexpected flag not supported"
        exit 1
        ;;
    esac
  done
fi

echo -e "
Welcome to the Cloud Command Line Interface Installer\n
"

if [[ -n "${CLI_ON_MACOS-}" ]]
then
  curl -O -fsSL https://aliyuncli.alicdn.com/aliyun-cli-macosx-"$VERSION"-universal.tgz
  tar zxf aliyun-cli-macosx-"$VERSION"-universal.tgz
  mv ./aliyun /usr/local/bin/
fi

if [[ -n "${CLI_ON_LINUX-}" ]]
then
  UNAME_MACHINE="$(/usr/bin/uname -m)"
  if [[ "${UNAME_MACHINE}" == "arm64" || "${UNAME_MACHINE}" == "aarch64" ]]
  then
    curl -O -fsSL https://aliyuncli.alicdn.com/aliyun-cli-linux-"$VERSION"-arm64.tgz
    tar zxf aliyun-cli-linux-"$VERSION"-arm64.tgz
  else
    curl -O -fsSL https://aliyuncli.alicdn.com/aliyun-cli-linux-"$VERSION"-amd64.tgz
    tar zxf aliyun-cli-linux-"$VERSION"-amd64.tgz
  fi
  mv ./aliyun /usr/local/bin/
fi
