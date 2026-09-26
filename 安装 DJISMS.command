#!/bin/zsh
cd "${0:A:h}" || exit 1
/usr/local/bin/python3 install.py
result=$?
echo "按回车关闭窗口"
read reply
exit $result
