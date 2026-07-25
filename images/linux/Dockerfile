FROM ubuntu:24.04@sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        bash-completion \
        build-essential \
        ca-certificates \
        curl \
        dnsutils \
        file \
        git \
        iproute2 \
        iputils-ping \
        jq \
        less \
        locales \
        netcat-openbsd \
        openssh-server \
        procps \
        python3 \
        python3-pip \
        ripgrep \
        rsync \
        sudo \
        tmux \
        unzip \
        vim-tiny \
    && rm -rf /var/lib/apt/lists/* \
    && if ! id ubuntu >/dev/null 2>&1; then useradd --create-home --shell /bin/bash ubuntu; fi \
    && passwd --delete ubuntu \
    && echo "ubuntu ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/ubuntu \
    && chmod 0440 /etc/sudoers.d/ubuntu

COPY entrypoint.sh /usr/local/bin/tempvm-entrypoint
COPY sshd_config /etc/ssh/sshd_config

EXPOSE 2222

ENTRYPOINT ["/usr/local/bin/tempvm-entrypoint"]
