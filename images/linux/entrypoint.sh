#!/usr/bin/env bash
set -euo pipefail

readonly credentials_dir=/run/tempvm-credentials
readonly authorized_keys="${credentials_dir}/authorized_keys"
readonly user_home=/home/ubuntu
readonly host_key=/run/tempvm/ssh_host_ed25519_key

if [[ ! -s "${authorized_keys}" ]]; then
  echo "missing ${authorized_keys}" >&2
  exit 1
fi

install -d -m 0755 /run/sshd /run/tempvm
install -d -m 0700 -o ubuntu -g ubuntu "${user_home}/.ssh"
install -m 0600 -o ubuntu -g ubuntu "${authorized_keys}" "${user_home}/.ssh/authorized_keys"
ssh-keygen -q -t ed25519 -N "" -f "${host_key}"

exec /usr/sbin/sshd -D -e -f /etc/ssh/sshd_config
