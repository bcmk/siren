package main

type translationKey int

const (
	start translationKey = iota
	online
	offline
	syntaxAdd
	syntaxRemove
	syntaxFeedback
	invalidSymbols
	alreadyAdded
	maxModels
	addError
	modelAdded
	modelNotInList
	modelRemoved
	donation
	feedbackThankYou
	sourceCode
	unknownCommand
	languages
)

var (
	langRu = []string{
		online:           "%s в сети",
		offline:          "%s не в сети",
		syntaxAdd:        "Формат команды: /add <i>идентификатор модели</i>\nИдентификатор модели можно посмотреть в адресной строке браузера",
		syntaxRemove:     "Формат команды: /remove <i>идентификатор модели</i>\nИдентификатор модели можно посмотреть в адресной строке браузера",
		syntaxFeedback:   "Формат команды: /feedback <i>сообщение</i>",
		invalidSymbols:   "Идентификатор модели %s содержит неподдерживаемые символы",
		alreadyAdded:     "Модель %s уже в вашем списке",
		maxModels:        "Можно добавить не более %d моделей",
		addError:         "Не получилось добавить модель %s\nПроверьте идентификатор модели или попробуйте позже\nФормат команды: /add <i>идентификатор модели</i>\nИдентификатор модели можно посмотреть в адресной строке браузера",
		modelAdded:       "Модель %s добавлена",
		modelNotInList:   "Модель %s не в вашем списке",
		modelRemoved:     "Модель %s удалена",
		donation:         "Хотите поддержать проект?\nBitcoin: 1PG5Th1vUQN1DkcHHAd21KA7CzwkMZwchE\nEthereum: 0x95af5ca0c64f3415431409926629a546a1bf99fc\nЕсли вы не знаете, что это такое, просто подарите моей любимой модели BBWebb 77тк",
		feedbackThankYou: "Спасибо за отклик!",
		sourceCode:       "Исходный код: https://github.com/bcmk/bcb",
		unknownCommand:   "Такой команде не обучен",
		languages:        "English bot: t.me/BongacamsOnlineBot",
		start: `Команды

__add__ _идентификатор модели_ — Добавить модель
__remove__ _идентификатор модели_ — Удалить модель
__list__ — Список подписок
__donate__ — Поддержать проект
__feedback__ _текст_ — Обратная связь
__source__ — Исходный код
__language__ — Этот бот на других языках`,
	}
	langEn = []string{
		online:           "%s online",
		offline:          "%s offline",
		syntaxAdd:        "Syntax: /add <i>model ID</i>\nYou can find a model ID in an address line of your browser",
		syntaxRemove:     "Syntax: /remove <i>model ID</i>\nYou can find a model ID in an address line of your browser",
		syntaxFeedback:   "Syntax: /feedback <i>message</i>",
		invalidSymbols:   "Model ID %s has invalid symbols",
		alreadyAdded:     "Model %s is already in your list",
		maxModels:        "You can add no more than %d models",
		addError:         "Could not add a model %s\nCheck a model ID or try later\nSyntax: /add <i>model ID</i>\nYou can find a model ID in an address line of your browser",
		modelAdded:       "Model %s added successfully",
		modelNotInList:   "Model %s is not in your list",
		modelRemoved:     "Model %s removed successfully",
		donation:         "Donations\nBitcoin: 1PG5Th1vUQN1DkcHHAd21KA7CzwkMZwchE\nEthereum: 0x95af5ca0c64f3415431409926629a546a1bf99fc\nIf you don't know what it is, just give to my favorite model BBWebb 77tkn",
		feedbackThankYou: "Thank you for your feedback!",
		sourceCode:       "Source code: https://github.com/bcmk/bcb",
		unknownCommand:   "Unknown command",
		languages:        "Русский бот: t.me/BongacamsSirenBot",
		start: `Commands

__add__ _model ID_ — Add model
__remove__ _model ID_ — Remove model
__list__ — Subscriptions list
__donate__ — Donation instructions
__feedback__ _text_ — Send feedback
__source__ — Show source code
__language__ — This bot in other languages`,
	}
)
