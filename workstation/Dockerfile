FROM debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8

ARG TARGETARCH

SHELL ["/bin/bash", "-euo", "pipefail", "-c"]

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        bash-completion=1:2.11-6 \
        ca-certificates=20250419~deb12u2 \
        curl=7.88.1-10+deb12u15 \
        bind9-dnsutils=1:9.18.49-1~deb12u3 \
        git=1:2.39.5-0+deb12u3 \
        iputils-ping=3:20221126-1+deb12u1 \
        jq=1.6-2.1+deb12u3 \
        less=590-2.1~deb12u2 \
        netcat-openbsd=1.219-1 \
        openssh-client=1:9.2p1-2+deb12u10 \
        procps=2:4.0.2-3 \
        vim-tiny=2:9.0.1378-2+deb12u2 \
    && rm -rf /var/lib/apt/lists/* \
    && ln -s /usr/bin/vim.tiny /usr/local/bin/vim \
    && setcap -r /usr/bin/ping

COPY checksums.txt versions.env /tmp/workstation/

RUN set -a && . /tmp/workstation/versions.env && set +a \
    && case "${TARGETARCH}" in \
         amd64) KUBECTX_ARCH=x86_64; ASCIINEMA_ARCH=x86_64 ;; \
         arm64) KUBECTX_ARCH=arm64; ASCIINEMA_ARCH=aarch64 ;; \
         *) echo "unsupported TARGETARCH ${TARGETARCH}" >&2; exit 1 ;; \
       esac \
    && cd /tmp/workstation \
    && verify() { grep -F "  $1" checksums.txt | sha256sum -c -; } \
    && fetch() { curl -fsSL --retry 3 --proto '=https' --tlsv1.2 -o "$1" "$2"; } \
    && fetch "kubectl-linux-${TARGETARCH}" "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl" \
    && verify "kubectl-linux-${TARGETARCH}" \
    && install -m 0755 "kubectl-linux-${TARGETARCH}" /usr/local/bin/kubectl \
    && fetch "helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz" "https://get.helm.sh/helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz" \
    && verify "helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz" \
    && tar -xzf "helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz" --strip-components=1 "linux-${TARGETARCH}/helm" \
    && install -m 0755 helm /usr/local/bin/helm \
    && fetch "k9s_Linux_${TARGETARCH}.tar.gz" "https://github.com/derailed/k9s/releases/download/${K9S_VERSION}/k9s_Linux_${TARGETARCH}.tar.gz" \
    && verify "k9s_Linux_${TARGETARCH}.tar.gz" \
    && tar -xzf "k9s_Linux_${TARGETARCH}.tar.gz" k9s \
    && install -m 0755 k9s /usr/local/bin/k9s \
    && fetch "stern_${STERN_VERSION#v}_linux_${TARGETARCH}.tar.gz" "https://github.com/stern/stern/releases/download/${STERN_VERSION}/stern_${STERN_VERSION#v}_linux_${TARGETARCH}.tar.gz" \
    && verify "stern_${STERN_VERSION#v}_linux_${TARGETARCH}.tar.gz" \
    && tar -xzf "stern_${STERN_VERSION#v}_linux_${TARGETARCH}.tar.gz" stern \
    && install -m 0755 stern /usr/local/bin/stern \
    && fetch "kubectx_${KUBECTX_VERSION}_linux_${KUBECTX_ARCH}.tar.gz" "https://github.com/ahmetb/kubectx/releases/download/${KUBECTX_VERSION}/kubectx_${KUBECTX_VERSION}_linux_${KUBECTX_ARCH}.tar.gz" \
    && verify "kubectx_${KUBECTX_VERSION}_linux_${KUBECTX_ARCH}.tar.gz" \
    && tar -xzf "kubectx_${KUBECTX_VERSION}_linux_${KUBECTX_ARCH}.tar.gz" kubectx \
    && install -m 0755 kubectx /usr/local/bin/kubectx \
    && fetch "kubens_${KUBECTX_VERSION}_linux_${KUBECTX_ARCH}.tar.gz" "https://github.com/ahmetb/kubectx/releases/download/${KUBECTX_VERSION}/kubens_${KUBECTX_VERSION}_linux_${KUBECTX_ARCH}.tar.gz" \
    && verify "kubens_${KUBECTX_VERSION}_linux_${KUBECTX_ARCH}.tar.gz" \
    && tar -xzf "kubens_${KUBECTX_VERSION}_linux_${KUBECTX_ARCH}.tar.gz" kubens \
    && install -m 0755 kubens /usr/local/bin/kubens \
    && fetch "yq_linux_${TARGETARCH}" "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_${TARGETARCH}" \
    && verify "yq_linux_${TARGETARCH}" \
    && install -m 0755 "yq_linux_${TARGETARCH}" /usr/local/bin/yq \
    && fetch "asciinema-${ASCIINEMA_ARCH}-unknown-linux-gnu" "https://github.com/asciinema/asciinema/releases/download/${ASCIINEMA_VERSION}/asciinema-${ASCIINEMA_ARCH}-unknown-linux-gnu" \
    && verify "asciinema-${ASCIINEMA_ARCH}-unknown-linux-gnu" \
    && install -m 0755 "asciinema-${ASCIINEMA_ARCH}-unknown-linux-gnu" /usr/local/bin/asciinema \
    && cd / && rm -rf /tmp/workstation

RUN groupadd --gid 1000 learner \
    && useradd --uid 1000 --gid 1000 --create-home --home-dir /home/learner --shell /bin/bash learner \
    && mkdir -p /workspace /data /home/learner/.kube /etc/kubedrill /opt/exam/bin \
    && ln -s /usr/local/bin/kubectl /opt/exam/bin/kubectl \
    && ln -s /usr/local/bin/helm /opt/exam/bin/helm \
    && ln -s /usr/local/bin/yq /opt/exam/bin/yq \
    && ln -s /usr/local/bin/kubedrill /opt/exam/bin/kubedrill \
    && ln -s /usr/bin/vim.tiny /opt/exam/bin/vim \
    && chown -R learner:learner /workspace /data /home/learner \
    && chmod 0777 /home/learner /home/learner/.kube

COPY --chmod=0644 bashrc /etc/kubedrill/bashrc
COPY --chmod=0755 entrypoint.sh /usr/local/bin/entrypoint.sh

RUN printf '\n. /etc/kubedrill/bashrc\n' >> /home/learner/.bashrc \
    && chown -h learner:learner /home/learner/.bashrc

ENV KUBEDRILL_WORKSTATION=1 \
    KUBECONFIG=/home/learner/.kube/config \
    KUBEDRILL_ROOT=/workspace/repo \
    KUBEDRILL_DATA=/data \
    KUBE_EDITOR=vim \
    LANG=C.UTF-8

USER learner
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["shell"]
