export function confirmDialog({ title, description, confirmLabel = '确认删除' }) {
  return new Promise((resolve) => {
    const trigger = document.activeElement;
    const dialog = document.createElement('dialog');
    dialog.className = 'confirm-dialog';
    dialog.innerHTML = `
      <form method="dialog" class="dialog-card">
        <div class="dialog-icon" aria-hidden="true">!</div>
        <h2 id="dialog-title"></h2>
        <p id="dialog-description"></p>
        <div class="dialog-actions">
          <button class="button button--secondary" type="submit" value="cancel">取消</button>
          <button class="button button--danger" type="submit" value="confirm">${confirmLabel}</button>
        </div>
      </form>`;
    dialog.querySelector('#dialog-title').textContent = title;
    dialog.querySelector('#dialog-description').textContent = description;

    dialog.addEventListener('close', () => {
      const confirmed = dialog.returnValue === 'confirm';
      dialog.remove();
      // 对话框关闭后归还焦点，避免键盘用户失去当前位置。
      trigger?.focus?.();
      resolve(confirmed);
    }, { once: true });
    document.body.append(dialog);
    dialog.showModal();
    dialog.querySelector('.button--danger').focus();
  });
}
