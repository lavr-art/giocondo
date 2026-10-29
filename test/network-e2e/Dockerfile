FROM debian:12
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    systemd systemd-sysv openssh-server curl ca-certificates iptables iproute2 \
    procps python3 gnupg apt-utils openssl ufw >/dev/null \
 && echo 'root:network-test-only' | chpasswd \
 && sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config \
 && systemctl enable ssh \
 && systemctl mask nftables.service systemd-networkd-wait-online.service \
 && rm -f /etc/machine-id /var/lib/dbus/machine-id
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
