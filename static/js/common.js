  // Fix bootstrap backdrop stacking when having multiple modals
  $(document).on('show.bs.modal', '.modal', function() {
    const zIndex = 10 + Math.max(...Array.from(document.querySelectorAll('*')).map((el) => +el.style.zIndex));
    $(this).css('z-index', zIndex);
    setTimeout(() => $('.modal-backdrop').not('.modal-stack').css('z-index', zIndex - 1).addClass('modal-stack'));
  });
