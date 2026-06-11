include $(TOPDIR)/rules.mk

PKG_NAME:=lists-tg
PKG_VERSION:=1.0.0
PKG_RELEASE:=1

include $(INCLUDE_DIR)/package.mk

define Package/lists-tg
  SECTION:=utils
  CATEGORY:=Utilities
  TITLE:=Telegram bot for domain and IP lists
  DEPENDS:=+ca-bundle
endef

define Package/lists-tg/description
  Lightweight Telegram bot to manage domain_list.lst and ip_list.lst on OpenWrt.
endef

define Build/Configure
endef

define Build/Compile
endef

define Package/lists-tg/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) ./files/usr/bin/lists-tg $(1)/usr/bin/lists-tg
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/etc/config/lists-tg $(1)/etc/config/lists-tg
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/etc/init.d/lists-tg $(1)/etc/init.d/lists-tg
	$(INSTALL_DIR) $(1)/etc/lists-tg
	$(INSTALL_DIR) $(1)/var/lib/lists-tg
endef

$(eval $(call BuildPackage,lists-tg))
