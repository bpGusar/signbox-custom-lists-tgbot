include $(TOPDIR)/rules.mk

PKG_NAME:=luci-app-lists-tg
PKG_VERSION:=1.0.0
PKG_RELEASE:=1

PKG_LICENSE:=GPL-2.0-or-later

include $(INCLUDE_DIR)/package.mk

define Package/luci-app-lists-tg
	SECTION:=luci
	CATEGORY:=LuCI
	SUBMENU:=3. Applications
	TITLE:=Lists Telegram Bot
	DEPENDS:=+luci-base +lists-tg
	PKGARCH:=all
endef

define Package/luci-app-lists-tg/description
	LuCI interface for Lists Telegram Bot.
endef

define Build/Configure
endef

define Build/Compile
endef

define Package/luci-app-lists-tg/install
	$(INSTALL_DIR) $(1)/www/luci-static/resources/view/lists-tg
	$(CP) ./htdocs/luci-static/resources/view/lists-tg/lists-tg.js \
		$(1)/www/luci-static/resources/view/lists-tg/
	$(INSTALL_DIR) $(1)/usr/share/luci/menu.d
	$(INSTALL_DATA) ./root/usr/share/luci/menu.d/luci-app-lists-tg.json \
		$(1)/usr/share/luci/menu.d/
	$(INSTALL_DIR) $(1)/usr/share/rpcd/acl.d
	$(INSTALL_DATA) ./root/usr/share/rpcd/acl.d/luci-app-lists-tg.json \
		$(1)/usr/share/rpcd/acl.d/
endef

$(eval $(call BuildPackage,luci-app-lists-tg))
