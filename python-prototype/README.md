# python-prototype（早期原型）

这是项目的第一版，用 Python + pynput 实现，功能已被根目录的 Go 版完全取代。
保留在此处仅作参考。

运行方式（需要 Python 3.8+）：

```bash
pip install -r requirements.txt
python key_monitor.py
```

与 Go 版的主要差异：无托盘图标、无鼠标按键统计、面板样式相同但由 Python 进程提供、
数据同为 SQLite 但表结构一致可互相读取。
