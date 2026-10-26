# Firecracker rootfs builder. Lima keeps a bootable cloud disk; build-lima.sh
# applies the same dependency installer to that disk offline.
ARG BASE_IMAGE
FROM ${BASE_IMAGE}
ARG AXV_GUEST_PACKAGE_SOURCE=preserve
COPY configure-package-sources /usr/local/lib/axv/configure-package-sources
COPY install-base.sh /usr/local/lib/axv/install-base
COPY package-inventory.sh /usr/local/lib/axv/package-inventory
COPY package-inventory.py /usr/local/lib/axv/package-inventory.py
RUN AXV_GUEST_PACKAGE_SOURCE="$AXV_GUEST_PACKAGE_SOURCE" sh /usr/local/lib/axv/install-base firecracker \
    && rm /usr/local/lib/axv/install-base \
    && printf 'axv ALL=(root) NOPASSWD: /sbin/reboot\n' > /etc/sudoers.d/axv-shutdown \
    && chmod 0440 /etc/sudoers.d/axv-shutdown
COPY axv-guest /usr/local/bin/axv-guest
COPY image.json /usr/share/axv/image.json
COPY guest-distribution/ /usr/share/doc/axv-guest/
COPY prepare-guest.sh /usr/local/lib/axv/prepare-guest
COPY prepare-ssh /usr/local/lib/axv/prepare-ssh
COPY prepare-tasks.py /usr/local/lib/axv/prepare-tasks
COPY axv-ssh.service /etc/systemd/system/
COPY axv-prepare.service axv-guest.service /etc/systemd/system/
RUN chmod 0755 /usr/local/bin/axv-guest /usr/local/lib/axv/prepare-guest /usr/local/lib/axv/prepare-ssh /usr/local/lib/axv/prepare-tasks \
    && mkdir -p /etc/systemd/system/axv-ssh.service.d \
    && printf '[Unit]\nRequires=axv-prepare.service\nAfter=axv-prepare.service\n' > /etc/systemd/system/axv-ssh.service.d/prepare.conf \
    && systemctl enable axv-prepare.service axv-guest.service axv-ssh.service \
    && systemctl mask ssh.service ssh.socket systemd-networkd.service systemd-resolved.service \
    && systemctl set-default multi-user.target
CMD ["/sbin/init"]
