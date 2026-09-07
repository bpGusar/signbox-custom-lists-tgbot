include $(TOPDIR)/rules.mk

PKG_NAME:=lst-signbox-lists-tgbot
PKG_VERSION:=$(if $(LISTS_TG_VERSION),$(LISTS_TG_VERSION),0.$(shell date -u +%Y%m%d).0)
PKG_RELEASE:=$(if $(LISTS_TG_RELEASE),$(LISTS_TG_RELEASE),1)

include $(INCLUDE_DIR)/package.mk

define Package/lst-signbox-lists-tgbot
  SECTION:=utils
  CATEGORY:=Utilities
  TITLE:=Telegram bot for domain and IP lists
  DEPENDS:=+ca-bundle +wget
endef

define Package/lst-signbox-lists-tgbot/description
  Lightweight Telegram bot to manage domain_list.lst and ip_list.lst on OpenWrt.
endef

define Package/lst-signbox-lists-tgbot/conffiles
/etc/config/lst-signbox-lists-tgbot
endef

define Build/Configure
endef

define Build/Compile
endef

define Package/lst-signbox-lists-tgbot/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) ./files/usr/bin/lst-signbox-lists-tgbot $(1)/usr/bin/lst-signbox-lists-tgbot
	$(INSTALL_DIR) $(1)/usr/sbin
	$(INSTALL_BIN) ./files/usr/sbin/lst-signbox-lists-tgbot-upgrade $(1)/usr/sbin/lst-signbox-lists-tgbot-upgrade
	$(INSTALL_BIN) ./files/usr/sbin/lst-frp-setup $(1)/usr/sbin/lst-frp-setup
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/etc/config/lst-signbox-lists-tgbot $(1)/etc/config/lst-signbox-lists-tgbot
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/etc/init.d/lst-signbox-lists-tgbot $(1)/etc/init.d/lst-signbox-lists-tgbot
	$(INSTALL_BIN) ./files/etc/init.d/frpc $(1)/etc/init.d/frpc
	$(INSTALL_DIR) $(1)/etc/lst-signbox-lists-tgbot
	$(INSTALL_DIR) $(1)/var/lib/lst-signbox-lists-tgbot
endef

# Runs from opkg on every install/update. The IPKG_INSTROOT guard keeps it from
# executing on the build host when an image is assembled offline. setsid
# detaches the run so it survives opkg killing the postinst process group, and
# it is backgrounded so a slow download never holds up the install. lst-frp-setup
# reads its own UCI config, is idempotent, and does nothing unless frp_enabled=1.
define Package/lst-signbox-lists-tgbot/postinst
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] && exit 0
if command -v setsid >/dev/null 2>&1; then
	setsid /usr/sbin/lst-frp-setup ensure >/dev/null 2>&1 &
else
	/usr/sbin/lst-frp-setup ensure >/dev/null 2>&1 &
fi
exit 0
endef

$(eval $(call BuildPackage,lst-signbox-lists-tgbot))
