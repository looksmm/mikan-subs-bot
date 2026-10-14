# Writes internal/tgbot/plus_textlist.go: the built-in texts the addon's page may change.
#   python3 tools/texts_gen.py internal/tgbot/plus_textlist.go
import sys
G = [
 ("Кнопки", [
  ("back","Назад"),("cancel","Отмена"),("support","Поддержка (кнопка на экранах покупки)"),("openPage","Открыть страницу подписки"),
  ("subscriptions","Подписки (когда их несколько)"),("promo","Промокоды"),("buy","Купить подписку"),("trial","Пробный период бесплатно"),
  ("trialPaid","Пробный период за деньги, %s — цена"),("trialOpen","Открыть подписку (после пробного)"),("buyTraffic","Докупить трафик"),
  ("removeSub","Убрать подписку из бота"),("yesRemove","Да, убрать"),("yesUnbind","Да, отвязать"),
  ("transferAllow","Разрешить перенос"),("transferDeny","Отказать в переносе"),("channelJoin","Подписаться на канал"),("channelCheck","Проверить подписку на канал"),
  ("payStars","Оплата Stars, %s — цена"),("payCard","Оплата картой, %s — цена"),("payCrypto","Оплата криптой, %s — цена"),
  ("payAddon","Другой способ оплаты: %s — название, %s — цена"),("payButton","Оплатить, %s — сумма"),
  ("menuApp","Кнопка Mini App у поля ввода"),("commands","Описание /start в меню команд"),
 ]),
 ("Подписка", [
  ("subTitle","Заголовок экрана подписки, %s — её имя"),("stateActive","Состояние: работает"),("stateExpiring","Состояние: скоро закончится"),
  ("stateLimited","Состояние: трафик закончился"),("stateExpired","Состояние: закончилась"),("stateOff","Состояние: приостановлена"),
  ("forever","Срок: бессрочно"),("termUntil","Срок: до даты, %s — дата, %s — сколько осталось"),("noLimit","Трафик: без лимита"),
  ("trafficOf","Трафик: %s из %s"),("trafficNoLimit","Трафик без лимита, %s — израсходовано"),("resets","Когда обновится трафик, %s — дата"),
  ("poolOut","Пул трафика закончился"),("plusPackages","Лимит с пакетами: %s + %s"),("switchTitle","Выбор подписки"),
  ("confirmRemove","Убрать подписку из бота? %s — имя"),("removed","Подписка убрана, %s — имя"),("noSub","Подписка не найдена"),
 ]),
 ("Устройства", [
  ("devicesTitle","Заголовок экрана"),("devicesNone","Устройств ещё нет"),("devicesOff","Привязка выключена, %s — лимит"),("devicesNote","Подсказка под списком"),
  ("confirmUnbind","Отвязать устройство? %s — имя"),("unbound","Устройство отвязано, %s — имя"),("wait","Следующее можно отвязать, %s — когда"),
  ("unbindLeft","Сколько ещё можно отвязать: %d из %d за %s"),("returnNote","Пауза после отвязки, %s — сколько"),("confirmReturn","Подтверждение с паузой, %s — сколько"),
  ("unbindFailed","Не получилось отвязать"),("unbindOff","Отвязать может только админ"),("returnNever","Вернуть может только админ"),("confirmNever","Подтверждение: вернуть может только админ"),
  ("sharedPlace","Общее место приложений без ID"),("device","Устройство без имени"),
 ]),
 ("Подключение", [
  ("connectTitle","Заголовок экрана «Подключить устройство»"),("connectText","Инструкция, %s — ссылка подписки"),
 ]),
 ("Привязка подписки к Telegram", [
  ("linked","Подписка подключена, %s — имя"),("alreadyLinked","Подписка уже здесь, %s — имя"),("linkExpired","Ссылка устарела"),("linkInvalid","Ссылка не подошла"),
  ("linkLimit","Слишком много подписок, %d — лимит"),("transferAsk","Владельцу: просят перенести, %s — подписка, %s — кто"),("transferAsked","Просящему: спросили владельца"),
  ("transferBusy","Уже есть запрос"),("transferDone","Владельцу: перенесено, %s — подписка"),("transferDenied","Владельцу: отказ записан, %s — подписка"),
  ("transferDeniedNew","Просящему: владелец отказал"),("transferStale","Запрос уже не действует"),("transferNotice","Просящему: подписка теперь здесь, %s — подписка"),
 ]),
 ("Покупка", [
  ("buyTitle","Заголовок списка тарифов"),("renewTitle","Заголовок продления, %s — подписка"),("pickTerm","Выбор срока"),("priceFrom","Цена «от», %s — цена"),
  ("payHow","Как оплатить?"),("invoice","Счёт: %s — тариф, %s — сумма, %s — что будет после оплаты"),("payNew","…подписка будет готова"),("payRenew","…подписка продлится"),
  ("notForSale","Тариф больше не продаётся"),("payUnavailable","Оплата недоступна"),("tooManyInvoices","Слишком много счетов"),
  ("paidNew","Оплачено: новая подписка, %s — имя, %s — тариф, %s — ссылка"),("paidRenew","Оплачено: продлено, %s — имя, %s — до какого числа"),
  ("trafficTitle","Заголовок пакетов трафика, %s — подписка"),("payPackage","…трафик начислится"),("packageGone","Пакет больше не продаётся"),("paidPackage","Оплачен пакет: %s — пакет, %s — подписка"),
 ]),
 ("Пробный период и бесплатный тариф", [
  ("trialDone","Пробный выдан, %s — имя"),("trialUsed","Пробный уже был"),("trialOff","Пробный недоступен"),("trialFail","Не получилось выдать пробный"),
  ("trialTitle","Заголовок платного пробного"),("trialNotGiven","Платный пробный не выдан, %s — сумма"),
  ("freeDone","Бесплатный выдан, %s — имя"),("freeActive","Бесплатный уже есть"),("freeOff","Бесплатный недоступен"),("freeFail","Не получилось выдать бесплатный"),
  ("freeChannel","Бесплатный для подписчиков канала"),("channelNotYet","Пока не видим в канале"),
 ]),
 ("Возвраты", [
  ("refundedNew","Возврат новой подписки: %s — тариф, %s — подписка"),("refundedRenew","Возврат продления: %s — тариф, %s — подписка, %s — новый срок"),
  ("refundedPackage","Возврат пакета: %s — пакет, %s — подписка"),("refundedGone","Возврат, подписки уже нет: %s — что"),
 ]),
]
out = ["// Code generated by tools/texts_gen.py: the built-in texts the addon's page may change. DO NOT EDIT.","","package tgbot","",
"// builtinTexts are mikan's words the admin may replace, by group, with their fields.",
"var builtinTexts = []textField{"]
for g, items in G:
    for k, t in items:
        out.append(f'\t{{key: "{k}", group: "{g}", title: "{t}", field: func(w *words) *string {{ return &w.{k} }}}},')
out.append("}")
open(sys.argv[1],"w").write("\n".join(out)+"\n")
print(sum(len(i) for _,i in G), "texts")
