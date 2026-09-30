# Changelog

Здесь — что изменилось в каждой версии: простыми словами для человека, со ссылкой на задачу и на
изменение, которыми строка пришла в код.

Формат — [Keep a Changelog 1.1.0](https://keepachangelog.com/ru/1.1.0/), версии —
[SemVer](https://semver.org/lang/ru/). Разделы версий идут от новых к старым, а строки внутри
раздела — в том порядке, в котором изменения вливались.

**Правило.** Задача, которая меняет то, что человек увидит или сможет сделать, добавляет строку в
раздел «Не выпущено» в том же PR, где меняется поведение (правило 3 в
[PROJECT_RULES.md](PROJECT_RULES.md)). Строка пишется для человека и не копируется из заголовка
коммита; из коммитов журнал не собирается.

**Первый выпуск — `v0.1.0`**, при закрытии вехи `v1`. До него всё слитое лежит в «Не выпущено».
Как выпустить версию — в [README](README.md#releases) и в `scripts/release.sh`.

## [Не выпущено]

### Добавлено

- `crewflow task list` — список прогонов проекта: идущие сверху, а у каждого номер, число попыток,
  чем кончилась последняя, когда и как долго шла, и какое изменение она открыла.
  ([#5](https://github.com/naghuale/crewflow/pull/5), задача [#1](https://github.com/naghuale/crewflow/issues/1))
- `crewflow auth app import` и `crewflow auth app check` — внести ключ GitHub App в связку ключей
  macOS и проверить, что у установки именно те права, которые нужны прогону.
  ([#11](https://github.com/naghuale/crewflow/pull/11), задача [#3](https://github.com/naghuale/crewflow/issues/3))
- `crewflow task list` говорит, какой это проект и что ещё бежит на этой машине, а `-all` показывает
  все проекты сразу, из любой папки.
  ([#19](https://github.com/naghuale/crewflow/pull/19), задача [#10](https://github.com/naghuale/crewflow/issues/10))
- `crewflow review` — ворота слияния в коде: можно ли слить это изменение, а если нельзя — одна
  причина из таблицы §7h и что с ней делать.
  ([#24](https://github.com/naghuale/crewflow/pull/24), задача [#7](https://github.com/naghuale/crewflow/issues/7))
- Таблица списка читается с одного взгляда: колонки выровнены, заголовок не режется посреди слова,
  цвет — только в терминале, без него и в файле и в трубе остаются буквы.
  ([#27](https://github.com/naghuale/crewflow/pull/27), задача [#20](https://github.com/naghuale/crewflow/issues/20))
- Прогон, остановившийся на известной привычке (`/tmp`, папка рядом с рабочей копией), продолжается
  один раз сам, в той же сессии, с текстом о том, что делать.
  ([#30](https://github.com/naghuale/crewflow/pull/30), задача [#26](https://github.com/naghuale/crewflow/issues/26))
- `crewflow merge` — слияние ровно одобренного коммита, одним refspec, без `--force`; и
  `crewflow verify` — проверка после слияния: ветка на том коммите, задача закрыта, CI зелёный.
  ([#42](https://github.com/naghuale/crewflow/pull/42), задача [#8](https://github.com/naghuale/crewflow/issues/8))
- `CHANGELOG.md` — этот файл.
  ([#69](https://github.com/naghuale/crewflow/pull/69), задача [#69](https://github.com/naghuale/crewflow/issues/69))

### Изменено

- Ворота слияния описаны спецификацией, по которой пишутся тесты: что хранится, что вычисляется, кто
  может одобрить, какие причины отказа бывают.
  ([#4](https://github.com/naghuale/crewflow/pull/4))
- Исполнитель самого crewflow работает как бот своего GitHub App, а не как владелец.
  ([#12](https://github.com/naghuale/crewflow/pull/12))
- README отвечает сначала «зачем это», потом «как это работает», и говорит честно, что ещё в работе.
  ([#16](https://github.com/naghuale/crewflow/pull/16))
- Меры процесса и бюджет CI расписаны формулами, с правилом «метрики объясняют, а не ранжируют».
  ([#23](https://github.com/naghuale/crewflow/pull/23))
- Откуда взялось правило «CI — ограниченный ресурс»: числа за сентябрь по прогонам, минутам и долям
  macOS.
  ([#25](https://github.com/naghuale/crewflow/pull/25))
- Ошибки оркестратора записаны уроками, и каждый урок закрыт контролем.
  ([#28](https://github.com/naghuale/crewflow/pull/28))
- Матрица ответственности: кто что может, чем каждая граница удерживается и где вместо проверки
  осталось доверие.
  ([#33](https://github.com/naghuale/crewflow/pull/33))
- README называет роли, зоны доступа и здоровье процесса; подробности режимов ушли в дизайн, с
  ссылкой на него.
  ([#34](https://github.com/naghuale/crewflow/pull/34))
- Сказано, что входит в ядро, и названы вехи `v1` и `v1.1`: задача в ядре только тогда, когда без неё
  ломается путь одной задачи.
  ([#35](https://github.com/naghuale/crewflow/pull/35))
- Тесты ворот слияния описаны в §7h как обещания, которые может прочитать человек, а не как номера
  тестов; реестр обещаний сверяется с разделом автоматически.
  ([#36](https://github.com/naghuale/crewflow/pull/36))
- В правилах проекта: документ — то же, что код, и его никто не вливает сам; сказано, где правда
  (репозиторий, хостинг, журнал практики); задача вне вехи ссылается на факт журнала или на пункт
  вехи.
  ([#39](https://github.com/naghuale/crewflow/pull/39), задача [#38](https://github.com/naghuale/crewflow/issues/38))
- Правило 4: дефект, переживший три неудачные проверки правки, следующей правкой не закрывается, пока
  не появился новый факт — и перечислено, что фактом считается.
  ([#46](https://github.com/naghuale/crewflow/pull/46), задача [#44](https://github.com/naghuale/crewflow/issues/44))

### Исправлено

- Ответы GitHub читаются такими, какими GitHub их даёт: каждое поле сверено с документацией REST API
  версии `2022-11-28`, и неизвестный тип поля больше не молча ломает проверку.
  ([#14](https://github.com/naghuale/crewflow/pull/14), задача [#13](https://github.com/naghuale/crewflow/issues/13))
- Токен App запрашивается по имени репозитория, как это описано у GitHub, а не по `owner/name`;
  `doctor` спрашивает у App пробный токен и выбрасывает его, чтобы отказ GitHub был виден до задачи.
  ([#17](https://github.com/naghuale/crewflow/pull/17), задача [#15](https://github.com/naghuale/crewflow/issues/15))
- `verify` читает уже слитое изменение, а не то, которое ещё можно слить: слитая на другой машине
  правка больше не проходит проверку с выводом «эта машина не знала состояния задачи».
  ([#48](https://github.com/naghuale/crewflow/pull/48), задача [#43](https://github.com/naghuale/crewflow/issues/43))
- Тест наблюдения за прогоном больше не зависает, когда тик остался без читателя: тест ждёт конца
  наблюдения, а не отправки в канал.
  ([#71](https://github.com/naghuale/crewflow/pull/71), задача [#47](https://github.com/naghuale/crewflow/issues/47))
- Отсутствие правил ветки на бесплатном плане больше не значит «хостинг недоступен»: ворота берут
  проверки из `crewflow.toml` и говорят в отчёте, что правил нет.
  ([#72](https://github.com/naghuale/crewflow/pull/72), задача [#66](https://github.com/naghuale/crewflow/issues/66))
- `crewflow merge` закрывает задачу сам, когда хостинг не закрыл её по `Closes #N`, и говорит в
  отчёте, кто закрыл; при отказе ворот не закрывается ничего.
  ([#75](https://github.com/naghuale/crewflow/pull/75), задача [#70](https://github.com/naghuale/crewflow/issues/70))

### Безопасность

- Исполнитель читает только те папки, которые проект назвал командами (`[access] read_from`), и не
  может прочитать секрет — даже если проект назвал его сам.
  ([#6](https://github.com/naghuale/crewflow/pull/6), задача [#2](https://github.com/naghuale/crewflow/issues/2))
- Режим `[identity] mode = "bot"`: исполнитель работает как бот GitHub App с часовым токеном на один
  репозиторий и четырьмя правами, ключ лежит в связке ключей macOS, а `pre-push` пропускает только
  ветку задачи и не трогает рабочую копию человека.
  ([#11](https://github.com/naghuale/crewflow/pull/11), задача [#3](https://github.com/naghuale/crewflow/issues/3))
- Отказ на секрете никогда не причина продолжить прогон: он заканчивается `blocked-secret`, и
  crewflow не возобновляет такой прогон сам.
  ([#30](https://github.com/naghuale/crewflow/pull/30), задача [#26](https://github.com/naghuale/crewflow/issues/26))

[Не выпущено]: https://github.com/naghuale/crewflow/commits/main