include $(TOPDIR)/rules.mk

PKG_NAME:=luci-app-lst-signbox-lists-tgbot
PKG_VERSION:=$(if $(LISTS_TG_VERSION),$(LISTS_TG_VERSION),0.$(shell date -u +%Y%m%d).0)
PKG_RELEASE:=$(if $(LISTS_TG_RELEASE),$(LISTS_TG_RELEASE),1)

PKG_LICENSE:=GPL-2.0-or-later

include $(INCLUDE_DIR)/package.mk

define Package/luci-app-lst-signbox-lists-tgbot
	SECTION:=luci
	CATEGORY:=LuCI
	SUBMENU:=3. Applications
	TITLE:=Lists Telegram Bot
	DEPENDS:=+luci-base +lst-signbox-lists-tgbot
	PKGARCH:=all
endef

define Package/luci-app-lst-signbox-lists-tgbot/description
	LuCI interface for Lists Telegram Bot.
endef

define Build/Configure
endef

define Build/Compile
endef

define Package/luci-app-lst-signbox-lists-tgbot/install
	$(INSTALL_DIR) $(1)/www/luci-static/resources/view/lst-signbox-lists-tgbot
	$(CP) ./htdocs/luci-static/resources/view/lst-signbox-lists-tgbot/lst-signbox-lists-tgbot.js \
		$(1)/www/luci-static/resources/view/lst-signbox-lists-tgbot/
	$(INSTALL_DIR) $(1)/usr/share/luci/menu.d
	$(INSTALL_DATA) ./root/usr/share/luci/menu.d/luci-app-lst-signbox-lists-tgbot.json \
		$(1)/usr/share/luci/menu.d/
	$(INSTALL_DIR) $(1)/usr/share/rpcd/acl.d
	$(INSTALL_DATA) ./root/usr/share/rpcd/acl.d/luci-app-lst-signbox-lists-tgbot.json \
		$(1)/usr/share/rpcd/acl.d/
endef

$(eval $(call BuildPackage,luci-app-lst-signbox-lists-tgbot))
